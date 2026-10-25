use crate::model::{ConnectionInfo, PveGuest, PveInfo};
use crate::util::{command_exists, read_trim, run, short_hostname, truncate};
use serde_json::Value;
use std::path::Path;
use std::time::Duration;

const IP_LOOKUP_LIMIT: usize = 30;

pub fn collect() -> PveInfo {
    let node = short_hostname();
    if !Path::new("/etc/pve").exists() {
        return PveInfo {
            available: false,
            node,
            version: None,
            error: None,
            vms: Vec::new(),
            containers: Vec::new(),
        };
    }

    let version = run("pveversion", &[], Duration::from_secs(5))
        .ok()
        .filter(|output| output.success)
        .and_then(|output| output.stdout.lines().next().map(|line| line.trim().to_string()))
        .filter(|line| !line.is_empty());

    let mut errors = Vec::new();
    let mut lookups = 0usize;
    let vms = collect_guests("qemu", &node, &mut errors, &mut lookups);
    let containers = collect_guests("lxc", &node, &mut errors, &mut lookups);

    PveInfo {
        available: true,
        node,
        version,
        error: if errors.is_empty() {
            None
        } else {
            Some(errors.join("; "))
        },
        vms,
        containers,
    }
}

fn collect_guests(
    kind: &str,
    node: &str,
    errors: &mut Vec<String>,
    lookups: &mut usize,
) -> Vec<PveGuest> {
    let path = format!("/nodes/{node}/{kind}");
    let mut guests = Vec::new();
    if command_exists("pvesh") {
        match run(
            "pvesh",
            &["get", path.as_str(), "--output-format", "json"],
            Duration::from_secs(20),
        ) {
            Ok(output) if output.success => {
                if let Some(items) = json_array(&output.stdout) {
                    for item in items {
                        if let Some(guest) = guest_from_json(&item, kind) {
                            guests.push(enrich(guest, kind, lookups));
                        }
                    }
                    return guests;
                }
                errors.push(format!("{kind}: pvesh 输出不是 JSON"));
            }
            Ok(output) => errors.push(format!(
                "{kind}: pvesh 失败 ({}) {}",
                output.code.unwrap_or(-1),
                truncate(output.stderr.trim(), 160)
            )),
            Err(error) => errors.push(format!("{kind}: pvesh 无法执行: {error}")),
        }
    }

    let (program, args) = if kind == "qemu" {
        ("qm", vec!["list"])
    } else {
        ("pct", vec!["list"])
    };
    if !command_exists(program) {
        if !errors.iter().any(|item| item.contains(kind)) {
            errors.push(format!("{kind}: 未找到 pvesh 或 {program}"));
        }
        return guests;
    }
    match run(program, &args, Duration::from_secs(15)) {
        Ok(output) if output.success => {
            for guest in parse_list_output(kind, &output.stdout) {
                guests.push(enrich(guest, kind, lookups));
            }
        }
        Ok(output) => errors.push(format!(
            "{kind}: {program} list 失败 ({}) {}",
            output.code.unwrap_or(-1),
            truncate(output.stderr.trim(), 160)
        )),
        Err(error) => errors.push(format!("{kind}: {program} 无法执行: {error}")),
    }
    guests
}

fn enrich(mut guest: PveGuest, kind: &str, lookups: &mut usize) -> PveGuest {
    let config_path = if kind == "qemu" {
        format!("/etc/pve/qemu-server/{}.conf", guest.vmid)
    } else {
        format!("/etc/pve/lxc/{}.conf", guest.vmid)
    };
    let config = parse_config(&read_trim(&config_path).unwrap_or_default());
    if guest.ostype.is_empty() {
        guest.ostype = config.get("ostype").cloned().unwrap_or_default();
    }
    guest.os_family = os_family(&guest.ostype);
    if guest.cpu_cores.is_none() {
        guest.cpu_cores = config
            .get("cores")
            .and_then(|value| value.trim().parse::<u64>().ok());
    }
    if guest.memory_bytes.is_none() {
        guest.memory_bytes = config
            .get("memory")
            .and_then(|value| value.trim().parse::<u64>().ok())
            .map(|mb| mb * 1024 * 1024);
    }
    if guest.disk_bytes.is_none() {
        guest.disk_bytes = ["rootfs", "scsi0", "virtio0", "ide0"]
            .iter()
            .find_map(|key| config.get(*key))
            .and_then(|value| size_from_config(value));
    }
    if guest.tags.is_empty() {
        guest.tags = config
            .get("tags")
            .map(|value| split_tags(value))
            .unwrap_or_default();
    }
    if guest.ip_addresses.is_empty() && guest.status == "running" && *lookups < IP_LOOKUP_LIMIT {
        *lookups += 1;
        guest.ip_addresses = if kind == "qemu" {
            qemu_ips(guest.vmid, &config)
        } else {
            lxc_ips(guest.vmid, &config)
        };
    }
    guest.connection = connection_for(&guest);
    guest
}

fn guest_from_json(item: &Value, kind: &str) -> Option<PveGuest> {
    let vmid = value_u64(item.get("vmid")?)?;
    let name = item
        .get("name")
        .and_then(Value::as_str)
        .unwrap_or_default()
        .to_string();
    let status = item
        .get("status")
        .and_then(Value::as_str)
        .unwrap_or("unknown")
        .to_lowercase();
    Some(PveGuest {
        vmid,
        name: if name.is_empty() {
            format!("{kind}-{vmid}")
        } else {
            name
        },
        kind: kind.to_string(),
        status,
        ostype: String::new(),
        os_family: "unknown".to_string(),
        cpu_cores: item.get("cpus").and_then(value_u64),
        memory_bytes: item.get("maxmem").and_then(value_u64),
        disk_bytes: item.get("maxdisk").and_then(value_u64),
        uptime_seconds: item.get("uptime").and_then(value_u64),
        net_in_bytes: item.get("netin").and_then(value_u64),
        net_out_bytes: item.get("netout").and_then(value_u64),
        ip_addresses: Vec::new(),
        connection: None,
        tags: item.get("tags").map(value_tags).unwrap_or_default(),
    })
}

fn parse_list_output(kind: &str, output: &str) -> Vec<PveGuest> {
    let mut guests = Vec::new();
    for line in output.lines() {
        let fields: Vec<&str> = line.split_whitespace().collect();
        if fields.is_empty() {
            continue;
        }
        if fields[0].eq_ignore_ascii_case("VMID") || fields[0].eq_ignore_ascii_case("ID") {
            continue;
        }
        let Ok(vmid) = fields[0].parse::<u64>() else {
            continue;
        };
        if kind == "qemu" {
            let name = fields.get(1).copied().unwrap_or("").to_string();
            let status = fields.get(2).copied().unwrap_or("unknown").to_lowercase();
            let memory_bytes = fields
                .get(3)
                .and_then(|value| value.parse::<u64>().ok())
                .map(|mb| mb * 1024 * 1024);
            let disk_bytes = fields
                .get(4)
                .and_then(|value| value.parse::<u64>().ok())
                .map(|gb| gb * 1024 * 1024 * 1024);
            guests.push(PveGuest {
                vmid,
                name: if name.is_empty() { format!("qemu-{vmid}") } else { name },
                kind: kind.to_string(),
                status,
                ostype: String::new(),
                os_family: "unknown".to_string(),
                cpu_cores: None,
                memory_bytes,
                disk_bytes,
                uptime_seconds: None,
                net_in_bytes: None,
                net_out_bytes: None,
                ip_addresses: Vec::new(),
                connection: None,
                tags: Vec::new(),
            });
        } else {
            let status = fields.get(1).copied().unwrap_or("unknown").to_lowercase();
            let name = fields.get(3..).map(|v| v.join(" ")).unwrap_or_default();
            guests.push(PveGuest {
                vmid,
                name: if name.is_empty() { format!("lxc-{vmid}") } else { name },
                kind: kind.to_string(),
                status,
                ostype: String::new(),
                os_family: "unknown".to_string(),
                cpu_cores: None,
                memory_bytes: None,
                disk_bytes: None,
                uptime_seconds: None,
                net_in_bytes: None,
                net_out_bytes: None,
                ip_addresses: Vec::new(),
                connection: None,
                tags: Vec::new(),
            });
        }
    }
    guests
}

fn json_array(output: &str) -> Option<Vec<Value>> {
    let start = output.find('[')?;
    let end = output.rfind(']')?;
    let value: Value = serde_json::from_str(&output[start..=end]).ok()?;
    Some(match value {
        Value::Array(items) => items,
        Value::Object(map) => map
            .get("data")
            .and_then(Value::as_array)
            .cloned()
            .unwrap_or_default(),
        _ => Vec::new(),
    })
}

fn parse_config(content: &str) -> std::collections::HashMap<String, String> {
    let mut map = std::collections::HashMap::new();
    for line in content.lines() {
        let line = line.trim();
        if line.is_empty() || line.starts_with('#') {
            continue;
        }
        if let Some((key, value)) = line.split_once(':') {
            map.insert(key.trim().to_string(), value.trim().to_string());
        }
    }
    map
}

fn qemu_ips(vmid: u64, config: &std::collections::HashMap<String, String>) -> Vec<String> {
    let agent_enabled = config
        .get("agent")
        .map(|value| {
            let value = value.to_lowercase();
            value.starts_with('1') || value.contains("enabled=1")
        })
        .unwrap_or(false);
    if !agent_enabled {
        return Vec::new();
    }
    let id = vmid.to_string();
    match run(
        "qm",
        &["guest", "cmd", id.as_str(), "network-get-interfaces"],
        Duration::from_secs(3),
    ) {
        Ok(output) if output.success => parse_qga_ips(&output.stdout),
        _ => Vec::new(),
    }
}

fn lxc_ips(vmid: u64, config: &std::collections::HashMap<String, String>) -> Vec<String> {
    let id = vmid.to_string();
    if let Ok(output) = run(
        "pct",
        &["exec", id.as_str(), "--", "ip", "-json", "addr"],
        Duration::from_secs(2),
    ) {
        if output.success {
            let ips = parse_lxc_ips(&output.stdout);
            if !ips.is_empty() {
                return ips;
            }
        }
    }
    config
        .get("net0")
        .map(|value| parse_static_ip(value))
        .unwrap_or_default()
}

fn parse_qga_ips(output: &str) -> Vec<String> {
    let start = match output.find('[') {
        Some(value) => value,
        None => return Vec::new(),
    };
    let end = match output.rfind(']') {
        Some(value) => value,
        None => return Vec::new(),
    };
    let Ok(value) = serde_json::from_str::<Value>(&output[start..=end]) else {
        return Vec::new();
    };
    let mut ips = Vec::new();
    if let Some(interfaces) = value.as_array() {
        for interface in interfaces {
            if let Some(addresses) = interface.get("ip-addresses").and_then(Value::as_array) {
                for address in addresses {
                    if let Some(ip) = address.get("ip-address").and_then(Value::as_str) {
                        push_ip(&mut ips, ip);
                    }
                }
            }
        }
    }
    ips
}

fn parse_lxc_ips(output: &str) -> Vec<String> {
    let start = match output.find('[') {
        Some(value) => value,
        None => return Vec::new(),
    };
    let end = match output.rfind(']') {
        Some(value) => value,
        None => return Vec::new(),
    };
    let Ok(value) = serde_json::from_str::<Value>(&output[start..=end]) else {
        return Vec::new();
    };
    let mut ips = Vec::new();
    if let Some(interfaces) = value.as_array() {
        for interface in interfaces {
            if interface.get("ifname").and_then(Value::as_str) == Some("lo") {
                continue;
            }
            if let Some(addresses) = interface.get("addr_info").and_then(Value::as_array) {
                for address in addresses {
                    if let Some(ip) = address.get("local").and_then(Value::as_str) {
                        push_ip(&mut ips, ip);
                    }
                }
            }
        }
    }
    ips
}

fn parse_static_ip(net: &str) -> Vec<String> {
    let mut ips = Vec::new();
    for part in net.split(',') {
        if let Some(value) = part.trim().strip_prefix("ip=") {
            let ip = value.split('/').next().unwrap_or("").trim();
            if !ip.is_empty() && ip != "dhcp" && ip != "manual" {
                push_ip(&mut ips, ip);
            }
        }
    }
    ips
}

fn push_ip(ips: &mut Vec<String>, ip: &str) {
    let ip = ip.trim();
    if ip.is_empty()
        || ip.starts_with("127.")
        || ip == "::1"
        || ip.starts_with("169.254.")
        || ip.to_lowercase().starts_with("fe80")
        || ip == "0.0.0.0"
    {
        return;
    }
    if !ips.iter().any(|current| current == ip) {
        ips.push(ip.to_string());
    }
}

fn connection_for(guest: &PveGuest) -> Option<ConnectionInfo> {
    let host = guest
        .ip_addresses
        .iter()
        .find(|ip| ip.contains('.'))
        .or_else(|| guest.ip_addresses.first())?
        .clone();
    let (protocol, port) = match guest.os_family.as_str() {
        "windows" => ("rdp", 3389),
        "linux" => ("ssh", 22),
        _ => ("unknown", 0),
    };
    Some(ConnectionInfo {
        protocol: protocol.to_string(),
        host,
        port,
        note: "由 PVE 配置/Guest Agent 推断，连接前请确认网络和凭据".to_string(),
    })
}

fn os_family(ostype: &str) -> String {
    let value = ostype.to_lowercase();
    if value.starts_with("win") || value.starts_with("w2k") {
        "windows".to_string()
    } else if [
        "l26", "l24", "linux", "ubuntu", "debian", "centos", "fedora", "alpine", "arch",
        "opensuse", "rocky", "alma", "rhel", "oracle", "suse",
    ]
    .iter()
    .any(|needle| value.contains(needle))
    {
        "linux".to_string()
    } else {
        "unknown".to_string()
    }
}

fn split_tags(value: &str) -> Vec<String> {
    value
        .split([',', ';'])
        .map(|tag| tag.trim())
        .filter(|tag| !tag.is_empty())
        .map(|tag| tag.to_string())
        .collect()
}

fn value_tags(value: &Value) -> Vec<String> {
    match value {
        Value::Array(items) => items
            .iter()
            .filter_map(Value::as_str)
            .map(|tag| tag.to_string())
            .collect(),
        Value::String(text) => split_tags(text),
        _ => Vec::new(),
    }
}

fn value_u64(value: &Value) -> Option<u64> {
    value
        .as_u64()
        .or_else(|| value.as_i64().and_then(|v| u64::try_from(v).ok()))
        .or_else(|| value.as_str().and_then(|v| v.trim().parse().ok()))
}

fn size_from_config(value: &str) -> Option<u64> {
    for part in value.split(',') {
        if let Some(size) = part.trim().strip_prefix("size=") {
            return parse_size(size);
        }
    }
    None
}

fn parse_size(value: &str) -> Option<u64> {
    let value = value.trim();
    let (number, multiplier) = if let Some(v) = value.strip_suffix('T') {
        (v, 1024_u64.pow(4))
    } else if let Some(v) = value.strip_suffix('G') {
        (v, 1024_u64.pow(3))
    } else if let Some(v) = value.strip_suffix('M') {
        (v, 1024_u64.pow(2))
    } else if let Some(v) = value.strip_suffix('K') {
        (v, 1024_u64)
    } else {
        (value, 1)
    };
    number
        .trim()
        .parse::<f64>()
        .ok()
        .map(|v| (v * multiplier as f64).round() as u64)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn maps_pve_ostypes() {
        assert_eq!(os_family("win11"), "windows");
        assert_eq!(os_family("w2k22"), "windows");
        assert_eq!(os_family("l26"), "linux");
        assert_eq!(os_family("ubuntu"), "linux");
        assert_eq!(os_family("other"), "unknown");
    }

    #[test]
    fn parses_qga_ips() {
        let output = r#"[{"name":"eth0","ip-addresses":[{"ip-address":"10.0.0.4","ip-address-type":"ipv4","prefix":24},{"ip-address":"fe80::1","ip-address-type":"ipv6","prefix":64}]}]"#;
        assert_eq!(parse_qga_ips(output), vec!["10.0.0.4".to_string()]);
    }

    #[test]
    fn parses_lxc_ips() {
        let output = r#"[{"ifname":"lo","addr_info":[{"local":"127.0.0.1","family":"inet"}]},{"ifname":"eth0","addr_info":[{"local":"10.0.0.5","family":"inet"}]}]"#;
        assert_eq!(parse_lxc_ips(output), vec!["10.0.0.5".to_string()]);
    }

    #[test]
    fn parses_pvesh_guest() {
        let value: Value = serde_json::from_str(
            r#"{"vmid":101,"name":"win-test","status":"running","cpus":4,"maxmem":8589934592,"maxdisk":107374182400,"uptime":3600,"netin":10,"netout":20,"tags":"prod,rdp"}"#,
        )
        .unwrap();
        let guest = guest_from_json(&value, "qemu").expect("guest");
        assert_eq!(guest.vmid, 101);
        assert_eq!(guest.cpu_cores, Some(4));
        assert_eq!(guest.memory_bytes, Some(8589934592));
        assert_eq!(guest.tags, vec!["prod", "rdp"]);
    }

    #[test]
    fn parses_qm_list() {
        let output = "      VMID NAME                 STATUS     MEM(MB)    BOOTDISK(GB) PID\n       101 win-test             running        8192           100 1234\n";
        let guests = parse_list_output("qemu", output);
        assert_eq!(guests.len(), 1);
        assert_eq!(guests[0].name, "win-test");
        assert_eq!(guests[0].status, "running");
        assert_eq!(guests[0].memory_bytes, Some(8192 * 1024 * 1024));
    }

    #[test]
    fn parses_static_ip() {
        assert_eq!(
            parse_static_ip("virtio=AA:BB:CC:DD:EE:FF,bridge=vmbr0,ip=10.0.0.9/24"),
            vec!["10.0.0.9".to_string()]
        );
    }
}