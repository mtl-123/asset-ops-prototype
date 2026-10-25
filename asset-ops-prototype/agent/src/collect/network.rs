use crate::model::{NetworkInfo, NetworkInterface};
use std::collections::{HashMap, HashSet};
use std::fs;
use std::sync::{Mutex, OnceLock};
use std::time::Instant;

#[derive(Debug, Clone, Copy, Default)]
struct Counters {
    received_bytes: u64,
    transmitted_bytes: u64,
    packets_received: u64,
    packets_transmitted: u64,
    receive_errors: u64,
    transmit_errors: u64,
    receive_dropped: u64,
    transmit_dropped: u64,
}

#[derive(Clone)]
struct Snapshot {
    at: Instant,
    counters: HashMap<String, Counters>,
}

static SNAPSHOT: OnceLock<Mutex<Option<Snapshot>>> = OnceLock::new();

/// Collect byte counters from /sys/class/net. The selected interfaces are read
/// directly from the kernel's 64-bit statistics files; rates are computed from
/// two monotonic samples, never from wall-clock time.
pub fn collect(configured: &str) -> NetworkInfo {
    let requested = split_interfaces(configured);
    let available = list_interfaces();
    let (selected, selection, mut error) = select_interfaces(&available, &requested);
    if selected.is_empty() {
        return NetworkInfo {
            selection,
            selected_interfaces: Vec::new(),
            interfaces: Vec::new(),
            total_received_bytes: 0,
            total_transmitted_bytes: 0,
            total_received_bps: None,
            total_transmitted_bps: None,
            sample_interval_seconds: None,
            error: Some(error.unwrap_or_else(|| "没有可统计的网卡".to_string())),
        };
    }

    let mut counters = HashMap::new();
    for name in &selected {
        match read_counters(name) {
            Some(value) => {
                counters.insert(name.clone(), value);
            }
            None => {
                let message = format!("读取 {name} 的 /sys/class/net 统计失败");
                error = Some(match error {
                    Some(existing) => existing + "; " + &message,
                    None => message,
                });
            }
        }
    }

    let now = Instant::now();
    let state = SNAPSHOT.get_or_init(|| Mutex::new(None));
    let mut guard = state.lock().unwrap_or_else(|poisoned| poisoned.into_inner());
    let previous = guard.take();
    let interval = previous
        .as_ref()
        .map(|snapshot| now.saturating_duration_since(snapshot.at).as_secs_f64())
        .filter(|value| *value > 0.0);
    let mut rates: HashMap<String, (Option<f64>, Option<f64>)> = HashMap::new();
    for (name, current) in &counters {
        let pair = previous.as_ref().and_then(|snapshot| {
            let elapsed = interval?;
            let old = snapshot.counters.get(name)?;
            let rx = delta(current.received_bytes, old.received_bytes)? as f64 / elapsed;
            let tx = delta(current.transmitted_bytes, old.transmitted_bytes)? as f64 / elapsed;
            Some((Some(rx), Some(tx)))
        });
        rates.insert(name.clone(), pair.unwrap_or((None, None)));
    }
    *guard = Some(Snapshot {
        at: now,
        counters: counters.clone(),
    });
    drop(guard);

    let mut interfaces = Vec::new();
    let mut total_received = 0_u64;
    let mut total_transmitted = 0_u64;
    for name in &selected {
        let Some(value) = counters.get(name) else {
            continue;
        };
        let (received_bps, transmitted_bps) =
            rates.get(name).copied().unwrap_or((None, None));
        total_received = total_received.saturating_add(value.received_bytes);
        total_transmitted = total_transmitted.saturating_add(value.transmitted_bytes);
        interfaces.push(NetworkInterface {
            name: name.clone(),
            received_bytes: value.received_bytes,
            transmitted_bytes: value.transmitted_bytes,
            packets_received: value.packets_received,
            packets_transmitted: value.packets_transmitted,
            receive_errors: value.receive_errors,
            transmit_errors: value.transmit_errors,
            receive_dropped: value.receive_dropped,
            transmit_dropped: value.transmit_dropped,
            received_bps,
            transmitted_bps,
        });
    }
    let all_rates = interfaces
        .iter()
        .all(|interface| interface.received_bps.is_some() && interface.transmitted_bps.is_some());
    let total_received_bps = if interfaces.is_empty() {
        Some(0.0)
    } else if all_rates {
        Some(interfaces.iter().filter_map(|i| i.received_bps).sum())
    } else {
        None
    };
    let total_transmitted_bps = if interfaces.is_empty() {
        Some(0.0)
    } else if all_rates {
        Some(interfaces.iter().filter_map(|i| i.transmitted_bps).sum())
    } else {
        None
    };

    NetworkInfo {
        selection,
        selected_interfaces: selected,
        interfaces,
        total_received_bytes: total_received,
        total_transmitted_bytes: total_transmitted,
        total_received_bps,
        total_transmitted_bps,
        sample_interval_seconds: interval,
        error,
    }
}

fn list_interfaces() -> Vec<String> {
    let mut names = Vec::new();
    if let Ok(entries) = fs::read_dir("/sys/class/net") {
        for entry in entries.flatten() {
            if let Ok(name) = entry.file_name().into_string() {
                names.push(name);
            }
        }
    }
    names.sort();
    names
}

fn select_interfaces(
    available: &[String],
    requested: &[String],
) -> (Vec<String>, String, Option<String>) {
    if !requested.is_empty() {
        let available_set: HashSet<&str> = available.iter().map(String::as_str).collect();
        let missing: Vec<String> = requested
            .iter()
            .filter(|name| !available_set.contains(name.as_str()))
            .cloned()
            .collect();
        let selected: Vec<String> = requested
            .iter()
            .filter(|name| available_set.contains(name.as_str()))
            .cloned()
            .collect();
        let error = if missing.is_empty() {
            None
        } else {
            Some(format!("指定的网卡不存在：{}", missing.join(", ")))
        };
        return (selected, "manual".to_string(), error);
    }

    let mut physical: Vec<String> = available
        .iter()
        .filter(|name| is_physical(name))
        .cloned()
        .collect();
    // A bond/team already aggregates its slaves. Select the aggregate only, or
    // the physical slaves would be counted twice.
    let aggregates: Vec<String> = physical
        .iter()
        .filter(|name| name.starts_with("bond") || name.starts_with("team"))
        .cloned()
        .collect();
    if !aggregates.is_empty() {
        physical = aggregates;
    }
    if physical.is_empty() {
        physical = available
            .iter()
            .filter(|name| !is_virtual(name))
            .cloned()
            .collect();
    }
    (physical, "auto".to_string(), None)
}

fn split_interfaces(value: &str) -> Vec<String> {
    let mut seen = HashSet::new();
    value
        .split([',', ' ', ';'])
        .map(str::trim)
        .filter(|name| !name.is_empty())
        .filter(|name| seen.insert((*name).to_string()))
        .map(str::to_string)
        .collect()
}

fn is_virtual(name: &str) -> bool {
    name == "lo"
        || [
            "veth", "tap", "fw", "docker", "br-", "virbr", "vnet", "qvb", "qvo", "vmbr",
            "vunl", "lxcbr", "lxdbr",
        ]
        .iter()
        .any(|prefix| name.starts_with(prefix))
}

fn is_physical(name: &str) -> bool {
    !is_virtual(name)
        && ["eth", "en", "em", "ib", "wlan", "wwan", "bond", "team", "p1p"]
            .iter()
            .any(|prefix| name.starts_with(prefix))
}

fn read_counters(name: &str) -> Option<Counters> {
    let base = format!("/sys/class/net/{name}/statistics");
    Some(Counters {
        received_bytes: read_u64(&base, "rx_bytes")?,
        transmitted_bytes: read_u64(&base, "tx_bytes")?,
        packets_received: read_u64(&base, "rx_packets").unwrap_or(0),
        packets_transmitted: read_u64(&base, "tx_packets").unwrap_or(0),
        receive_errors: read_u64(&base, "rx_errors").unwrap_or(0),
        transmit_errors: read_u64(&base, "tx_errors").unwrap_or(0),
        receive_dropped: read_u64(&base, "rx_dropped").unwrap_or(0),
        transmit_dropped: read_u64(&base, "tx_dropped").unwrap_or(0),
    })
}

fn read_u64(base: &str, name: &str) -> Option<u64> {
    fs::read_to_string(format!("{base}/{name}"))
        .ok()?
        .trim()
        .parse()
        .ok()
}

fn delta(current: u64, previous: u64) -> Option<u64> {
    if current >= previous {
        Some(current - previous)
    } else {
        // Interface reset or reboot: do not produce a huge negative/overflow rate.
        None
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn manual_selection_keeps_order_and_reports_missing() {
        let available = vec!["eno1".to_string(), "vmbr0".to_string(), "lo".to_string()];
        let requested = vec!["vmbr0".to_string(), "missing0".to_string()];
        let (selected, mode, error) = select_interfaces(&available, &requested);
        assert_eq!(selected, vec!["vmbr0"]);
        assert_eq!(mode, "manual");
        assert!(error.unwrap().contains("missing0"));
    }

    #[test]
    fn auto_selection_excludes_bridges_and_veth() {
        let available = vec![
            "eno1".to_string(),
            "eno2".to_string(),
            "vmbr0".to_string(),
            "veth100".to_string(),
            "lo".to_string(),
        ];
        let (selected, mode, error) = select_interfaces(&available, &[]);
        assert_eq!(selected, vec!["eno1", "eno2"]);
        assert_eq!(mode, "auto");
        assert!(error.is_none());
    }

    #[test]
    fn auto_selection_prefers_bond_over_slaves() {
        let available = vec![
            "eno1".to_string(),
            "eno2".to_string(),
            "bond0".to_string(),
        ];
        let (selected, _, _) = select_interfaces(&available, &[]);
        assert_eq!(selected, vec!["bond0"]);
    }

    #[test]
    fn rate_handles_counter_reset() {
        assert_eq!(delta(50, 100), None);
        assert_eq!(delta(150, 100), Some(50));
    }
}
