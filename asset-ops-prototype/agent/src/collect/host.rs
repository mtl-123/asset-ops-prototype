use crate::config::MetricsConfig;
use crate::model::{CpuInfo, DiskInfo, HostInfo, LoadInfo, MemoryInfo, VirtualizationInfo};
use crate::util::{read_trim, unix_to_rfc3339};
use sysinfo::{Disks, System, MINIMUM_CPU_UPDATE_INTERVAL};

pub fn collect(network_interfaces: &str, metrics_config: &MetricsConfig) -> HostInfo {
    let mut system = System::new_all();
    system.refresh_all();
    std::thread::sleep(MINIMUM_CPU_UPDATE_INTERVAL);
    system.refresh_cpu_usage();

    let hostname = System::host_name().unwrap_or_else(|| "unknown".to_string());
    let cpu_model = system
        .cpus()
        .first()
        .map(|cpu| cpu.brand().trim().to_string())
        .filter(|v| !v.is_empty())
        .unwrap_or_else(|| "unknown".to_string());
    let logical_cores = system.cpus().len();
    let physical_cores = system.physical_core_count().unwrap_or(logical_cores);
    let total_memory = system.total_memory();
    let used_memory = system.used_memory();
    let available_memory = system.available_memory();
    let load = System::load_average();

    let disks = Disks::new_with_refreshed_list();
    let mut disk_rows: Vec<DiskInfo> = disks
        .list()
        .iter()
        .filter_map(|disk| {
            let total = disk.total_space();
            if total == 0 {
                return None;
            }
            let available = disk.available_space().min(total);
            let used = total - available;
            Some(DiskInfo {
                mount: disk.mount_point().to_string_lossy().to_string(),
                filesystem: disk.file_system().to_string_lossy().to_string(),
                total_bytes: total,
                used_bytes: used,
                available_bytes: available,
                usage_percent: percent(used, total),
            })
        })
        .collect();
    disk_rows.sort_by(|a, b| a.mount.cmp(&b.mount));
    disk_rows.truncate(32);

    HostInfo {
        hostname,
        os_name: System::name().unwrap_or_else(|| "Linux".to_string()),
        os_version: System::os_version().unwrap_or_default(),
        kernel_version: System::kernel_version().unwrap_or_default(),
        arch: std::env::consts::ARCH.to_string(),
        virtualization: detect_virtualization(),
        boot_time: unix_to_rfc3339(System::boot_time()),
        uptime_seconds: System::uptime(),
        cpu: CpuInfo {
            model: cpu_model,
            physical_cores,
            logical_cores,
            usage_percent: round2(system.global_cpu_usage() as f64),
        },
        memory: MemoryInfo {
            total_bytes: total_memory,
            used_bytes: used_memory,
            available_bytes: available_memory,
            usage_percent: percent(used_memory, total_memory),
        },
        load: LoadInfo {
            one: round2(load.one),
            five: round2(load.five),
            fifteen: round2(load.fifteen),
        },
        disks: disk_rows,
        network: crate::collect::network::collect(network_interfaces),
        metrics: crate::collect::metrics::collect(metrics_config),
    }
}

fn detect_virtualization() -> VirtualizationInfo {
    let cpuinfo = std::fs::read_to_string("/proc/cpuinfo").unwrap_or_default();
    let dmi_vendor = read_trim("/sys/class/dmi/id/sys_vendor").unwrap_or_default();
    let product = read_trim("/sys/class/dmi/id/product_name").unwrap_or_default();
    let board = read_trim("/sys/class/dmi/id/board_vendor").unwrap_or_default();
    let combined = format!("{dmi_vendor} {product} {board}").to_lowercase();
    let hypervisor = if cpuinfo.to_lowercase().contains("hypervisor")
        || combined.contains("qemu")
        || combined.contains("kvm")
        || combined.contains("vmware")
        || combined.contains("virtualbox")
        || combined.contains("xen")
    {
        if combined.contains("vmware") {
            "vmware"
        } else if combined.contains("virtualbox") {
            "virtualbox"
        } else if combined.contains("microsoft") || combined.contains("hyper-v") {
            "hyperv"
        } else if combined.contains("xen") {
            "xen"
        } else {
            "kvm"
        }
    } else {
        "none"
    };

    let cgroup = std::fs::read_to_string("/proc/1/cgroup").unwrap_or_default();
    let container = if std::path::Path::new("/.dockerenv").exists()
        || cgroup.contains("docker")
        || cgroup.contains("containerd")
    {
        "docker"
    } else if cgroup.contains("kubepods") {
        "kubernetes"
    } else if cgroup.contains("lxc") {
        "lxc"
    } else {
        "none"
    };

    VirtualizationInfo {
        is_virtual_machine: hypervisor != "none",
        hypervisor: hypervisor.to_string(),
        container: container.to_string(),
    }
}

fn percent(used: u64, total: u64) -> f64 {
    if total == 0 {
        0.0
    } else {
        round2(used as f64 * 100.0 / total as f64)
    }
}

fn round2(value: f64) -> f64 {
    (value * 100.0).round() / 100.0
}
