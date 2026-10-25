use anyhow::{bail, Context, Result};
use serde::{Deserialize, Serialize};
use std::fs;
use std::os::unix::fs::PermissionsExt;
use std::path::Path;

#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(default)]
pub struct AgentConfig {
    pub server: String,
    pub token: String,
    pub interval: u64,
    pub stale_days: i64,
    pub network_interfaces: Vec<String>,
    pub host_id: String,
    pub metrics: MetricsConfig,
    pub audit: AuditConfig,
    pub logs: LogsConfig,
    pub dependencies: DependenciesConfig,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(default)]
pub struct MetricsConfig {
    pub cpu_detail: bool,
    pub context_switch: bool,
    pub memory: bool,
    pub swap: bool,
    pub disk: bool,
    pub inode: bool,
    pub disk_io: bool,
    pub tcp: bool,
    pub process: bool,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(default)]
pub struct AuditConfig {
    pub enabled: bool,
    pub history_limit: usize,
    pub failed_limit: usize,
    pub lastlog: bool,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(default)]
pub struct LogsConfig {
    pub enabled: bool,
    pub mode: String,
    pub paths: Vec<String>,
    pub business_paths: Vec<String>,
    pub max_bytes_per_cycle: usize,
    pub loki_url: String,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(default)]
pub struct DependenciesConfig {
    pub auto_install: bool,
    pub auditd: bool,
}

impl Default for AgentConfig {
    fn default() -> Self {
        Self {
            server: "http://127.0.0.1:8765".to_string(),
            token: String::new(),
            interval: 60,
            stale_days: 90,
            network_interfaces: Vec::new(),
            host_id: String::new(),
            metrics: MetricsConfig::default(),
            audit: AuditConfig::default(),
            logs: LogsConfig::default(),
            dependencies: DependenciesConfig::default(),
        }
    }
}

impl Default for MetricsConfig {
    fn default() -> Self {
        Self {
            cpu_detail: true,
            context_switch: true,
            memory: true,
            swap: true,
            disk: true,
            inode: true,
            disk_io: true,
            tcp: true,
            process: true,
        }
    }
}

impl Default for AuditConfig {
    fn default() -> Self {
        Self {
            enabled: true,
            history_limit: 200,
            failed_limit: 200,
            lastlog: true,
        }
    }
}

impl Default for LogsConfig {
    fn default() -> Self {
        Self {
            enabled: false,
            mode: "loki".to_string(),
            paths: vec![
                "/var/log/auth.log".to_string(),
                "/var/log/secure".to_string(),
                "/var/log/syslog".to_string(),
                "/var/log/messages".to_string(),
                "/var/log/dmesg".to_string(),
            ],
            business_paths: vec![
                "/var/log/nginx/access.log".to_string(),
                "/var/log/nginx/error.log".to_string(),
                "/var/log/mysql/slow.log".to_string(),
            ],
            max_bytes_per_cycle: 262_144,
            loki_url: String::new(),
        }
    }
}

impl Default for DependenciesConfig {
    fn default() -> Self {
        Self {
            auto_install: true,
            auditd: false,
        }
    }
}

impl AgentConfig {
    pub fn load(path: &Path) -> Result<Option<Self>> {
        if !path.exists() {
            return Ok(None);
        }
        let text = fs::read_to_string(path)
            .with_context(|| format!("读取配置 {}", path.display()))?;
        let config: Self = toml::from_str(&text)
            .with_context(|| format!("解析 TOML 配置 {}", path.display()))?;
        Ok(Some(config))
    }

    pub fn save(&self, path: &Path) -> Result<()> {
        if let Some(parent) = path.parent() {
            fs::create_dir_all(parent)
                .with_context(|| format!("创建配置目录 {}", parent.display()))?;
        }
        let text = toml::to_string_pretty(self).context("序列化 TOML 配置")?;
        fs::write(path, text).with_context(|| format!("写入配置 {}", path.display()))?;
        fs::set_permissions(path, fs::Permissions::from_mode(0o600))?;
        Ok(())
    }

    pub fn network_spec(&self) -> String {
        self.network_interfaces
            .iter()
            .map(|name| name.trim())
            .filter(|name| !name.is_empty())
            .collect::<Vec<_>>()
            .join(",")
    }

    pub fn validate(&self) -> Result<()> {
        if self.server.trim().is_empty() {
            bail!("server 不能为空");
        }
        if self.token.contains('\n') || self.token.contains('\r') {
            bail!("token 不能包含换行");
        }
        if self.interval < 5 {
            bail!("interval 不能小于 5 秒");
        }
        if self.audit.history_limit > 5000 || self.audit.failed_limit > 5000 {
            bail!("审计历史条数过大（最大 5000）");
        }
        if self.logs.max_bytes_per_cycle > 16 * 1024 * 1024 {
            bail!("日志单周期上限过大（最大 16 MiB）");
        }
        if !["loki", "agent"].contains(&self.logs.mode.as_str()) {
            bail!("logs.mode 只能是 loki 或 agent");
        }
        Ok(())
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn parses_full_config_and_defaults() {
        let text = r#"
server = "https://ops.example.com"
token = "abc"
interval = 30
stale_days = 120
network_interfaces = ["eno1", "bond0"]

[metrics]
cpu_detail = true
disk_io = true
tcp = false

[audit]
enabled = true
history_limit = 50

[logs]
enabled = true
mode = "agent"
max_bytes_per_cycle = 1024

[dependencies]
auto_install = false
auditd = true
"#;
        let config: AgentConfig = toml::from_str(text).expect("toml");
        assert_eq!(config.interval, 30);
        assert_eq!(config.network_spec(), "eno1,bond0");
        assert!(config.metrics.disk_io);
        assert!(!config.metrics.tcp);
        assert!(config.metrics.process, "missing flags use defaults");
        assert_eq!(config.audit.history_limit, 50);
        assert!(config.logs.enabled);
        assert!(!config.dependencies.auto_install);
        assert!(config.dependencies.auditd);
        config.validate().unwrap();
    }

    #[test]
    fn rejects_unsafe_config() {
        let mut config = AgentConfig::default();
        config.interval = 1;
        assert!(config.validate().is_err());
        config.interval = 60;
        config.logs.mode = "kafka".to_string();
        assert!(config.validate().is_err());
    }
}
