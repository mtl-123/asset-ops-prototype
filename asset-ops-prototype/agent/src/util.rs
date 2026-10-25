use anyhow::{Context, Result};
use chrono::{SecondsFormat, TimeZone, Utc};
use std::fs::{self, File};
use std::os::unix::fs::PermissionsExt;
use std::path::Path;
use std::process::{Command, Stdio};
use std::sync::atomic::{AtomicU64, Ordering};
use std::time::Duration;
use wait_timeout::ChildExt;

static TEMP_SEQ: AtomicU64 = AtomicU64::new(0);

#[derive(Debug, Clone)]
pub struct CommandOutput {
    pub stdout: String,
    pub stderr: String,
    pub success: bool,
    pub code: Option<i32>,
}

pub fn run(program: &str, args: &[&str], timeout: Duration) -> Result<CommandOutput> {
    let dir = std::env::temp_dir().join(format!(
        "qiyun-agent-{}-{}",
        std::process::id(),
        TEMP_SEQ.fetch_add(1, Ordering::Relaxed)
    ));
    fs::create_dir_all(&dir).with_context(|| format!("create temp dir {}", dir.display()))?;
    let stdout_path = dir.join("stdout");
    let stderr_path = dir.join("stderr");
    let stdout_file = File::create(&stdout_path)?;
    let stderr_file = File::create(&stderr_path)?;

    let mut command = Command::new(program);
    command
        .args(args)
        .env("LC_ALL", "C")
        .env("LANG", "C")
        .stdin(Stdio::null())
        .stdout(Stdio::from(stdout_file))
        .stderr(Stdio::from(stderr_file));
    let mut child = command
        .spawn()
        .with_context(|| format!("spawn {}", program))?;
    let status = child.wait_timeout(timeout)?;
    let timed_out = status.is_none();
    if timed_out {
        let _ = child.kill();
        let _ = child.wait();
    }
    let stdout = fs::read_to_string(&stdout_path).unwrap_or_default();
    let stderr = fs::read_to_string(&stderr_path).unwrap_or_default();
    let _ = fs::remove_dir_all(&dir);
    let code = status.and_then(|s| s.code());
    Ok(CommandOutput {
        stdout,
        stderr,
        success: status.map(|s| s.success()).unwrap_or(false),
        code,
    })
}

pub fn command_exists(name: &str) -> bool {
    let Some(path) = std::env::var_os("PATH") else {
        return false;
    };
    std::env::split_paths(&path).any(|dir| {
        let candidate = dir.join(name);
        candidate.is_file()
            && fs::metadata(&candidate)
                .map(|m| m.permissions().mode() & 0o111 != 0)
                .unwrap_or(false)
    })
}

pub fn parse_percent(value: &str) -> Option<f64> {
    let trimmed = value.trim().trim_end_matches('%').trim();
    trimmed.parse::<f64>().ok()
}

pub fn parse_cpu_cores(value: &str) -> Option<f64> {
    let value = value.trim();
    if let Some(milli) = value.strip_suffix('m') {
        return milli.parse::<f64>().ok().map(|v| v / 1000.0);
    }
    value.parse::<f64>().ok()
}

pub fn parse_quantity_bytes(value: &str) -> Option<u64> {
    let value = value.trim();
    if value.is_empty() {
        return None;
    }
    let (number, multiplier) = if let Some(v) = value.strip_suffix("Ki") {
        (v, 1024_f64)
    } else if let Some(v) = value.strip_suffix("Mi") {
        (v, 1024_f64.powi(2))
    } else if let Some(v) = value.strip_suffix("Gi") {
        (v, 1024_f64.powi(3))
    } else if let Some(v) = value.strip_suffix("Ti") {
        (v, 1024_f64.powi(4))
    } else if let Some(v) = value.strip_suffix('K') {
        (v, 1000_f64)
    } else if let Some(v) = value.strip_suffix('M') {
        (v, 1000_f64.powi(2))
    } else if let Some(v) = value.strip_suffix('G') {
        (v, 1000_f64.powi(3))
    } else if let Some(v) = value.strip_suffix('T') {
        (v, 1000_f64.powi(4))
    } else {
        (value, 1_f64)
    };
    number
        .trim()
        .parse::<f64>()
        .ok()
        .map(|v| (v * multiplier).round() as u64)
}

pub fn read_trim(path: impl AsRef<Path>) -> Option<String> {
    fs::read_to_string(path)
        .ok()
        .map(|v| v.trim().to_string())
        .filter(|v| !v.is_empty())
}

pub fn host_id() -> String {
    for path in ["/etc/machine-id", "/var/lib/dbus/machine-id"] {
        if let Some(value) = read_trim(path) {
            return value;
        }
    }
    hostname::get()
        .map(|v| v.to_string_lossy().to_string())
        .unwrap_or_else(|_| "unknown-host".to_string())
}

pub fn short_hostname() -> String {
    hostname::get()
        .map(|v| v.to_string_lossy().to_string())
        .unwrap_or_else(|_| "unknown".to_string())
        .split('.')
        .next()
        .unwrap_or("unknown")
        .to_string()
}

pub fn now_rfc3339() -> String {
    Utc::now().to_rfc3339_opts(SecondsFormat::Secs, true)
}

pub fn unix_to_rfc3339(seconds: u64) -> String {
    Utc.timestamp_opt(seconds as i64, 0)
        .single()
        .unwrap_or_else(Utc::now)
        .to_rfc3339_opts(SecondsFormat::Secs, true)
}

pub fn truncate(value: &str, limit: usize) -> String {
    let mut out = String::new();
    for ch in value.chars().take(limit) {
        out.push(ch);
    }
    if value.chars().count() > limit {
        out.push_str("...");
    }
    out
}
