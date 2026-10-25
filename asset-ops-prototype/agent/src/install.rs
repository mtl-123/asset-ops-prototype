use crate::config::AgentConfig;
use crate::deps;
use anyhow::{bail, Context, Result};
use std::fs;
use std::os::unix::fs::PermissionsExt;
use std::path::{Path, PathBuf};
use std::process::Command;

const BIN_PATH: &str = "/usr/local/bin/qiyun-agent";
const UNIT_PATH: &str = "/etc/systemd/system/qiyun-agent.service";

pub struct Options {
    pub config_path: PathBuf,
    pub config: AgentConfig,
    pub force_config: bool,
    pub install_deps: bool,
}

pub fn run(options: Options) -> Result<()> {
    if !deps::is_root()? {
        bail!("安装 systemd 服务需要 root：sudo qiyun-agent --install ...");
    }
    options.config.validate()?;
    if !command_available("systemctl") {
        bail!("找不到 systemctl；该安装方式只支持 systemd 系统");
    }

    let existing = AgentConfig::load(&options.config_path)?;
    if (existing.is_none() || options.force_config) && options.config.token.trim().is_empty() {
        bail!("首次安装或 --force-config 需要 --token（服务端 data/agent.token）");
    }
    let effective = existing.clone().unwrap_or_else(|| options.config.clone());
    effective.validate()?;
    if let Some(old) = &existing {
        if !options.force_config {
            if old.server != options.config.server || old.token != options.config.token {
                println!("检测到已有配置，保留 {}；若要更新 server/token，请加 --force-config。", options.config_path.display());
            } else {
                println!("检测到已有配置，跳过写入：{}", options.config_path.display());
            }
        }
    }

    deps::ensure(&effective, options.install_deps)?;

    let config_written = if existing.is_none() || options.force_config {
        options.config.save(&options.config_path)?;
        println!("已写入配置 {}", options.config_path.display());
        true
    } else {
        false
    };

    let unit_text = render_unit(&options.config_path);
    let unit_written = write_if_changed(Path::new(UNIT_PATH), &unit_text, 0o644)?;
    if unit_written {
        println!("已写入 systemd 单元 {UNIT_PATH}");
    }

    let binary_changed = install_binary()?;
    if binary_changed {
        println!("已更新二进制 {BIN_PATH}");
    }

    systemctl(&["daemon-reload"])?;
    systemctl(&["enable", "qiyun-agent"])?;

    let active = command_success("systemctl", &["is-active", "--quiet", "qiyun-agent"]);
    let changed = binary_changed || config_written || unit_written;
    if !active || changed {
        systemctl(&["restart", "qiyun-agent"])?;
        println!("已启动/重启 qiyun-agent");
    } else {
        println!("服务已在运行，配置/单元/二进制均无变化，跳过重启。");
    }

    if !command_success("systemctl", &["is-active", "--quiet", "qiyun-agent"]) {
        let status = output_text("systemctl", &["status", "--no-pager", "qiyun-agent"]);
        bail!("服务未能启动：\n{status}\njournalctl -u qiyun-agent -n 80 --no-pager");
    }
    let enabled = output_text("systemctl", &["is-enabled", "qiyun-agent"]);
    println!("配置：{}", options.config_path.display());
    println!("二进制：{BIN_PATH}");
    println!("单元：{UNIT_PATH}");
    println!("开机自启：{}", enabled.trim());
    println!("修改配置后：systemctl restart qiyun-agent");
    if effective.server.contains("127.0.0.1") || effective.server.contains("localhost") {
        println!("警告：server 是回环地址，PVE 只会在本机连接；跨机上报请填平台实际 HTTPS 地址。");
    }
    Ok(())
}

fn install_binary() -> Result<bool> {
    let target = Path::new(BIN_PATH);
    let current = std::env::current_exe().context("读取当前可执行文件路径")?;
    if current
        .canonicalize()
        .ok()
        .zip(target.canonicalize().ok())
        .map(|(left, right)| left == right)
        .unwrap_or(false)
    {
        return Ok(false);
    }
    let current_bytes = fs::read(&current)
        .with_context(|| format!("读取当前二进制 {}", current.display()))?;
    if let Ok(existing_bytes) = fs::read(target) {
        if existing_bytes == current_bytes {
            return Ok(false);
        }
    }
    if let Some(parent) = target.parent() {
        fs::create_dir_all(parent)?;
    }
    let temporary = Path::new("/usr/local/bin/.qiyun-agent.tmp");
    fs::write(temporary, &current_bytes).context("写入临时二进制")?;
    fs::set_permissions(temporary, fs::Permissions::from_mode(0o755))?;
    fs::rename(temporary, target).context("安装二进制")?;
    Ok(true)
}

fn write_if_changed(path: &Path, content: &str, mode: u32) -> Result<bool> {
    if let Ok(existing) = fs::read_to_string(path) {
        if existing == content {
            return Ok(false);
        }
    }
    if let Some(parent) = path.parent() {
        fs::create_dir_all(parent)?;
    }
    fs::write(path, content).with_context(|| format!("写入 {}", path.display()))?;
    fs::set_permissions(path, fs::Permissions::from_mode(mode))?;
    Ok(true)
}

fn render_unit(config_path: &Path) -> String {
    format!(
        "[Unit]\n\
Description=Qiyun Asset Ops Linux inventory agent\n\
After=network-online.target\n\
Wants=network-online.target\n\
\n\
[Service]\n\
Type=simple\n\
User=root\n\
ExecStart=/usr/local/bin/qiyun-agent --config \"{}\"\n\
Restart=always\n\
RestartSec=10\n\
\n\
[Install]\n\
WantedBy=multi-user.target\n",
        config_path.display()
    )
}

fn command_available(name: &str) -> bool {
    Command::new(name)
        .arg("--version")
        .output()
        .map(|output| output.status.success())
        .unwrap_or(false)
}

fn command_success(name: &str, args: &[&str]) -> bool {
    Command::new(name)
        .args(args)
        .status()
        .map(|status| status.success())
        .unwrap_or(false)
}

fn output_text(name: &str, args: &[&str]) -> String {
    Command::new(name)
        .args(args)
        .output()
        .map(|output| String::from_utf8_lossy(&output.stdout).to_string())
        .unwrap_or_default()
}

fn systemctl(args: &[&str]) -> Result<()> {
    let output = Command::new("systemctl")
        .args(args)
        .output()
        .with_context(|| format!("执行 systemctl {}", args.join(" ")))?;
    if !output.status.success() {
        bail!(
            "systemctl {} 失败：{}{}",
            args.join(" "),
            String::from_utf8_lossy(&output.stdout).trim(),
            String::from_utf8_lossy(&output.stderr).trim()
        );
    }
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn unit_contains_config_and_binary_paths() {
        let unit = render_unit(Path::new("/etc/qiyun-agent.toml"));
        assert!(unit.contains("ExecStart=/usr/local/bin/qiyun-agent --config \"/etc/qiyun-agent.toml\""));
        assert!(unit.contains("Restart=always"));
    }

    #[test]
    fn write_if_changed_is_idempotent() {
        let dir = std::env::temp_dir().join(format!("qiyun-install-test-{}", std::process::id()));
        let _ = fs::remove_dir_all(&dir);
        fs::create_dir_all(&dir).unwrap();
        let path = dir.join("unit");
        assert!(write_if_changed(&path, "same", 0o600).unwrap());
        assert!(!write_if_changed(&path, "same", 0o600).unwrap());
        assert_eq!(fs::read_to_string(&path).unwrap(), "same");
        let _ = fs::remove_dir_all(&dir);
    }
}
