use crate::config::AgentConfig;
use crate::util::command_exists;
use anyhow::{bail, Context, Result};
use std::process::Command;

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Manager {
    Apt,
    Dnf,
    Yum,
    Zypper,
    Apk,
    Pacman,
}

impl Manager {
    fn detect() -> Option<Self> {
        for (command, manager) in [
            ("apt-get", Manager::Apt),
            ("dnf", Manager::Dnf),
            ("yum", Manager::Yum),
            ("zypper", Manager::Zypper),
            ("apk", Manager::Apk),
            ("pacman", Manager::Pacman),
        ] {
            if command_exists(command) {
                return Some(manager);
            }
        }
        None
    }

    fn package(self, name: &'static str) -> &'static str {
        match (self, name) {
            (Manager::Apt, "util-linux") => "util-linux",
            (Manager::Apt, "curl") => "curl",
            (Manager::Apt, "audit") => "auditd",
            (Manager::Dnf | Manager::Yum, "util-linux") => "util-linux",
            (Manager::Dnf | Manager::Yum, "curl") => "curl",
            (Manager::Dnf | Manager::Yum, "audit") => "audit",
            (Manager::Zypper, "util-linux") => "util-linux",
            (Manager::Zypper, "curl") => "curl",
            (Manager::Zypper, "audit") => "audit",
            (Manager::Apk, "util-linux") => "util-linux",
            (Manager::Apk, "curl") => "curl",
            (Manager::Apk, "audit") => "audit",
            (Manager::Pacman, "util-linux") => "util-linux",
            (Manager::Pacman, "curl") => "curl",
            (Manager::Pacman, "audit") => "audit",
            _ => name,
        }
    }

    fn install(self, packages: &[&str]) -> Result<()> {
        if packages.is_empty() {
            return Ok(());
        }
        let mut command;
        let args: Vec<&str> = match self {
            Manager::Apt => {
                command = Command::new("apt-get");
                command.env("DEBIAN_FRONTEND", "noninteractive");
                let mut args = vec!["install", "-y", "--no-install-recommends"];
                args.extend_from_slice(packages);
                args
            }
            Manager::Dnf => {
                command = Command::new("dnf");
                let mut args = vec!["install", "-y"];
                args.extend_from_slice(packages);
                args
            }
            Manager::Yum => {
                command = Command::new("yum");
                let mut args = vec!["install", "-y"];
                args.extend_from_slice(packages);
                args
            }
            Manager::Zypper => {
                command = Command::new("zypper");
                let mut args = vec!["--non-interactive", "install"];
                args.extend_from_slice(packages);
                args
            }
            Manager::Apk => {
                command = Command::new("apk");
                let mut args = vec!["add", "--no-cache"];
                args.extend_from_slice(packages);
                args
            }
            Manager::Pacman => {
                command = Command::new("pacman");
                let mut args = vec!["-Sy", "--noconfirm"];
                args.extend_from_slice(packages);
                args
            }
        };
        let output = command
            .args(&args)
            .output()
            .with_context(|| format!("执行包管理器安装：{} {}", args[0], packages.join(" ")))?;
        if !output.status.success() {
            bail!(
                "安装依赖失败：{}{}",
                String::from_utf8_lossy(&output.stdout).trim(),
                String::from_utf8_lossy(&output.stderr).trim()
            );
        }
        Ok(())
    }
}

#[derive(Debug, Clone)]
pub struct Check {
    pub command: &'static str,
    pub package: &'static str,
    pub available: bool,
    pub required: bool,
}

pub fn required(config: &AgentConfig) -> Vec<Check> {
    let mut checks = vec![Check {
        command: "curl",
        package: "curl",
        available: command_exists("curl"),
        required: true,
    }];
    if config.audit.enabled {
        for (command, package, required) in [
            ("last", "wtmpdb", false),
            ("lastb", "util-linux-extra", false),
            ("lastlog", "lastlog2", false),
            ("who", "util-linux", false),
        ] {
            // lastlog was replaced by lastlog2 on newer Debian/Ubuntu.
            let available = command_exists(command)
                || (command == "lastlog" && command_exists("lastlog2"))
                || (command == "lastb" && command_exists("lslogins"));
            checks.push(Check {
                command,
                package,
                available,
                required,
            });
        }
    }
    if config.dependencies.auditd {
        checks.push(Check {
            command: "auditctl",
            package: "audit",
            available: command_exists("auditctl"),
            required: false,
        });
    }
    checks
}

pub fn check(config: &AgentConfig) -> Vec<Check> {
    required(config)
}

pub fn print_checks(config: &AgentConfig) {
    for item in check(config) {
        println!(
            "{:<10} {:<10} {:<4} {}",
            item.command,
            item.package,
            if item.required { "必需" } else { "可选" },
            if item.available { "已安装" } else { "缺失" }
        );
    }
}

pub fn ensure(config: &AgentConfig, explicit: bool) -> Result<()> {
    let checks = check(config);
    let missing: Vec<&Check> = checks.iter().filter(|item| !item.available).collect();
    if missing.is_empty() {
        println!("依赖检查通过，没有需要安装的包。");
        return Ok(());
    }
    for item in &missing {
        println!(
            "缺少 {}（包 {}，{}）",
            item.command,
            item.package,
            if item.required { "必需" } else { "可选" }
        );
    }
    let required_missing: Vec<&Check> = missing.iter().copied().filter(|item| item.required).collect();
    let optional_missing: Vec<&Check> = missing.iter().copied().filter(|item| !item.required).collect();
    let may_install = config.dependencies.auto_install || explicit;
    if !required_missing.is_empty() {
        if !may_install {
            bail!("缺少必需依赖；使用 --install-deps 显式安装，或在配置中开启 dependencies.auto_install");
        }
        if !is_root()? {
            bail!("安装系统依赖需要 root");
        }
        let manager = Manager::detect().context("未识别包管理器（支持 apt/dnf/yum/zypper/apk/pacman）")?;
        let mut packages: Vec<&str> = required_missing
            .iter()
            .map(|item| manager.package(item.package))
            .collect();
        packages.sort_unstable();
        packages.dedup();
        println!("使用包管理器安装必需依赖：{}", packages.join(", "));
        manager.install(&packages)?;
    }

    // lastb/lastlog are optional audit helpers, but on Ubuntu 26/Debian 13
    // they moved out of util-linux. Try the known replacement packages on
    // Debian-family systems; never fail the Agent install if they are absent.
    if may_install && !optional_missing.is_empty() && is_root()? {
        if let Some(manager) = Manager::detect() {
            let packages = optional_packages(manager, &optional_missing);
            if !packages.is_empty() {
                println!("尝试安装可选审计包：{}", packages.join(", "));
                if let Err(error) = manager.install(&packages) {
                    println!("可选审计包安装失败（不阻断 Agent）：{error}");
                }
            }
        }
    }

    let after = check(config);
    let still_required: Vec<&str> = after
        .iter()
        .filter(|item| item.required && !item.available)
        .map(|item| item.command)
        .collect();
    if !still_required.is_empty() {
        bail!("必需依赖安装后仍缺少：{}", still_required.join(", "));
    }
    let still_optional: Vec<&str> = after
        .iter()
        .filter(|item| !item.required && !item.available)
        .map(|item| item.command)
        .collect();
    if still_optional.is_empty() {
        println!("全部依赖检查通过。");
    } else {
        println!(
            "可选审计命令仍缺失：{}；不会阻断安装。",
            still_optional.join(", ")
        );
        if still_optional.contains(&"lastb") {
            println!("提示：新版 Debian/Ubuntu 可能不再提供 lastb；失败登录可查看 journalctl/auth 日志。");
        }
        if still_optional.contains(&"lastlog") {
            println!("提示：可安装 lastlog2 包，Agent 会回退使用 lastlog2 命令。");
        }
        if still_optional.contains(&"last") {
            println!("提示：可安装 wtmpdb 包以恢复 last/wtmp 历史。");
        }
    }
    Ok(())
}

fn optional_packages(manager: Manager, missing: &[&Check]) -> Vec<&'static str> {
    if manager != Manager::Apt {
        return Vec::new();
    }
    let mut packages = Vec::new();
    for item in missing {
        match item.command {
            "last" => packages.push("wtmpdb"),
            "lastb" => packages.push("util-linux-extra"),
            "lastlog" => packages.push("lastlog2"),
            _ => {}
        }
    }
    packages.sort_unstable();
    packages.dedup();
    packages
}

pub fn is_root() -> Result<bool> {
    let output = Command::new("id")
        .arg("-u")
        .output()
        .context("无法执行 id -u")?;
    Ok(String::from_utf8_lossy(&output.stdout).trim() == "0")
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn maps_package_names_per_family() {
        assert_eq!(Manager::Apt.package("audit"), "auditd");
        assert_eq!(Manager::Dnf.package("audit"), "audit");
        assert_eq!(Manager::Apk.package("util-linux"), "util-linux");
    }

    #[test]
    fn audit_commands_are_optional() {
        let config = AgentConfig::default();
        for item in required(&config) {
            if item.command == "curl" {
                assert!(item.required);
            } else {
                assert!(!item.required, "{} must not block install", item.command);
            }
        }
    }

    #[test]
    fn missing_checks_cover_curl_and_audit_tools() {
        let mut config = AgentConfig::default();
        config.audit.enabled = true;
        let names: Vec<&str> = required(&config).iter().map(|item| item.command).collect();
        assert!(names.contains(&"curl"));
        assert!(names.contains(&"last"));
        assert!(names.contains(&"lastb"));
    }
}
