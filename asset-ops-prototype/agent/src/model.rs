use serde::{Deserialize, Serialize};

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Report {
    pub schema_version: u32,
    pub agent_version: String,
    pub host_id: String,
    pub collected_at: String,
    pub host: HostInfo,
    pub users: UsersInfo,
    pub platforms: Platforms,
    #[serde(default)]
    pub logs: Vec<LogFileStatus>,
}

#[derive(Debug, Clone, Serialize, Deserialize, Default)]
pub struct LogFileStatus {
    pub path: String,
    pub exists: bool,
    pub readable: bool,
    pub size_bytes: u64,
    pub modified_at: Option<String>,
    pub error: Option<String>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct HostInfo {
    pub hostname: String,
    pub os_name: String,
    pub os_version: String,
    pub kernel_version: String,
    pub arch: String,
    pub virtualization: VirtualizationInfo,
    pub boot_time: String,
    pub uptime_seconds: u64,
    pub cpu: CpuInfo,
    pub memory: MemoryInfo,
    pub load: LoadInfo,
    pub disks: Vec<DiskInfo>,
    pub network: NetworkInfo,
    #[serde(default)]
    pub metrics: Option<LinuxMetrics>,
}

#[derive(Debug, Clone, Serialize, Deserialize, Default)]
pub struct LinuxMetrics {
    pub sample_interval_seconds: Option<f64>,
    pub cpu: Option<CpuDetail>,
    pub context_switches_per_second: Option<f64>,
    pub memory: Option<MemoryDetail>,
    pub swap: Option<SwapDetail>,
    pub swap_io: Option<SwapIo>,
    pub disks: Vec<DiskIo>,
    pub inodes: Vec<InodeInfo>,
    pub tcp: Option<TcpStates>,
    pub processes: Option<ProcessInfo>,
}

#[derive(Debug, Clone, Serialize, Deserialize, Default)]
pub struct CpuDetail {
    pub user_percent: Option<f64>,
    pub nice_percent: Option<f64>,
    pub system_percent: Option<f64>,
    pub idle_percent: Option<f64>,
    pub iowait_percent: Option<f64>,
    pub irq_percent: Option<f64>,
    pub softirq_percent: Option<f64>,
    pub steal_percent: Option<f64>,
    pub guest_percent: Option<f64>,
}

#[derive(Debug, Clone, Serialize, Deserialize, Default)]
pub struct MemoryDetail {
    pub total_bytes: u64,
    pub free_bytes: u64,
    pub available_bytes: u64,
    pub buffers_bytes: u64,
    pub cached_bytes: u64,
    pub used_bytes: u64,
    pub usage_percent: f64,
}

#[derive(Debug, Clone, Serialize, Deserialize, Default)]
pub struct SwapDetail {
    pub total_bytes: u64,
    pub free_bytes: u64,
    pub used_bytes: u64,
    pub usage_percent: f64,
}

#[derive(Debug, Clone, Serialize, Deserialize, Default)]
pub struct SwapIo {
    pub pages_in_per_second: Option<f64>,
    pub pages_out_per_second: Option<f64>,
    pub bytes_in_per_second: Option<f64>,
    pub bytes_out_per_second: Option<f64>,
}

#[derive(Debug, Clone, Serialize, Deserialize, Default)]
pub struct DiskIo {
    pub device: String,
    pub read_iops: Option<f64>,
    pub write_iops: Option<f64>,
    pub read_bytes_per_second: Option<f64>,
    pub write_bytes_per_second: Option<f64>,
    pub await_ms: Option<f64>,
    pub aqu_sz: Option<f64>,
    pub util_percent: Option<f64>,
}

#[derive(Debug, Clone, Serialize, Deserialize, Default)]
pub struct InodeInfo {
    pub mount: String,
    pub filesystem: String,
    pub total: u64,
    pub used: u64,
    pub free: u64,
    pub usage_percent: f64,
}

#[derive(Debug, Clone, Serialize, Deserialize, Default)]
pub struct TcpStates {
    pub established: u64,
    pub syn_sent: u64,
    pub syn_recv: u64,
    pub fin_wait1: u64,
    pub fin_wait2: u64,
    pub time_wait: u64,
    pub close: u64,
    pub close_wait: u64,
    pub last_ack: u64,
    pub listen: u64,
    pub closing: u64,
    pub total: u64,
}

#[derive(Debug, Clone, Serialize, Deserialize, Default)]
pub struct ProcessInfo {
    pub total: u64,
    pub threads: u64,
    pub zombies: u64,
    pub fd_allocated: u64,
    pub fd_max: u64,
    pub fd_usage_percent: f64,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct VirtualizationInfo {
    pub is_virtual_machine: bool,
    pub hypervisor: String,
    pub container: String,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct CpuInfo {
    pub model: String,
    pub physical_cores: usize,
    pub logical_cores: usize,
    pub usage_percent: f64,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct MemoryInfo {
    pub total_bytes: u64,
    pub used_bytes: u64,
    pub available_bytes: u64,
    pub usage_percent: f64,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct LoadInfo {
    pub one: f64,
    pub five: f64,
    pub fifteen: f64,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct DiskInfo {
    pub mount: String,
    pub filesystem: String,
    pub total_bytes: u64,
    pub used_bytes: u64,
    pub available_bytes: u64,
    pub usage_percent: f64,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct NetworkInfo {
    pub selection: String,
    pub selected_interfaces: Vec<String>,
    pub interfaces: Vec<NetworkInterface>,
    pub total_received_bytes: u64,
    pub total_transmitted_bytes: u64,
    pub total_received_bps: Option<f64>,
    pub total_transmitted_bps: Option<f64>,
    pub sample_interval_seconds: Option<f64>,
    pub error: Option<String>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct NetworkInterface {
    pub name: String,
    pub received_bytes: u64,
    pub transmitted_bytes: u64,
    pub packets_received: u64,
    pub packets_transmitted: u64,
    pub receive_errors: u64,
    pub transmit_errors: u64,
    pub receive_dropped: u64,
    pub transmit_dropped: u64,
    pub received_bps: Option<f64>,
    pub transmitted_bps: Option<f64>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct UsersInfo {
    pub logged_in_count: usize,
    pub session_count: usize,
    pub logged_in: Vec<LoginSession>,
    pub local_user_count: usize,
    pub stale_days: i64,
    pub stale_count: usize,
    pub stale: Vec<StaleUser>,
    pub never_logged_in: Vec<String>,
    pub lastlog_available: bool,
    #[serde(default)]
    pub history: Vec<LoginRecord>,
    #[serde(default)]
    pub failed_logins: Vec<FailedLogin>,
    #[serde(default)]
    pub history_available: bool,
    #[serde(default)]
    pub failed_available: bool,
    #[serde(default)]
    pub history_error: Option<String>,
    #[serde(default)]
    pub failed_error: Option<String>,
}

#[derive(Debug, Clone, Serialize, Deserialize, Default)]
pub struct LoginRecord {
    pub user: String,
    pub tty: String,
    pub host: String,
    pub started: String,
    pub ended: String,
    pub duration: String,
    pub kind: String,
    pub raw: String,
}

#[derive(Debug, Clone, Serialize, Deserialize, Default)]
pub struct FailedLogin {
    pub user: String,
    pub tty: String,
    pub host: String,
    pub time: String,
    pub raw: String,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct LoginSession {
    pub user: String,
    pub tty: String,
    pub host: String,
    pub login_time: String,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct StaleUser {
    pub user: String,
    pub last_login: Option<String>,
    pub days_inactive: Option<i64>,
    pub never: bool,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Platforms {
    pub pve: PveInfo,
    pub docker: DockerInfo,
    pub kubernetes: KubernetesInfo,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct PveInfo {
    pub available: bool,
    pub node: String,
    pub version: Option<String>,
    pub error: Option<String>,
    pub vms: Vec<PveGuest>,
    pub containers: Vec<PveGuest>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct PveGuest {
    pub vmid: u64,
    pub name: String,
    pub kind: String,
    pub status: String,
    pub ostype: String,
    pub os_family: String,
    pub cpu_cores: Option<u64>,
    pub memory_bytes: Option<u64>,
    pub disk_bytes: Option<u64>,
    pub uptime_seconds: Option<u64>,
    pub net_in_bytes: Option<u64>,
    pub net_out_bytes: Option<u64>,
    pub ip_addresses: Vec<String>,
    pub connection: Option<ConnectionInfo>,
    pub tags: Vec<String>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct ConnectionInfo {
    pub protocol: String,
    pub host: String,
    pub port: u16,
    pub note: String,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct DockerInfo {
    pub available: bool,
    pub version: Option<String>,
    pub error: Option<String>,
    pub images: Option<u64>,
    pub containers_total: usize,
    pub containers_running: usize,
    pub containers: Vec<DockerContainer>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct DockerContainer {
    pub id: String,
    pub name: String,
    pub image: String,
    pub state: String,
    pub status: String,
    pub ports: String,
    pub created_at: String,
    pub cpu_percent: Option<f64>,
    pub memory_usage: Option<String>,
    pub memory_percent: Option<f64>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct KubernetesInfo {
    pub available: bool,
    pub context: Option<String>,
    pub version: Option<String>,
    pub error: Option<String>,
    pub node_count: usize,
    pub nodes_ready: usize,
    pub nodes: Vec<K8sNode>,
    pub namespace_count: usize,
    pub pod_count: usize,
    pub pods_running: usize,
    pub pods_pending: usize,
    pub pods_failed: usize,
    pub pods_succeeded: usize,
    pub unhealthy_pods: Vec<K8sPod>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct K8sNode {
    pub name: String,
    pub ready: bool,
    pub roles: Vec<String>,
    pub kubelet_version: String,
    pub os: String,
    pub arch: String,
    pub cpu_cores: Option<f64>,
    pub memory_bytes: Option<u64>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct K8sPod {
    pub namespace: String,
    pub name: String,
    pub phase: String,
    pub node: String,
    pub restarts: u64,
    pub reason: String,
}