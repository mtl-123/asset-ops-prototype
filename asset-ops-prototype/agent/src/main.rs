mod collect;
mod config;
mod deps;
mod install;
mod model;
mod util;

use anyhow::{bail, Context, Result};
use clap::Parser;
use std::fs::{self, OpenOptions};
use std::io::Write;
use std::os::unix::fs::{DirBuilderExt, OpenOptionsExt};
use std::path::PathBuf;
use std::time::Duration;
use tracing_subscriber::EnvFilter;

use config::AgentConfig;

#[derive(Debug, Parser)]
#[command(
    name = "qiyun-agent",
    version,
    about = "Collect Linux/PVE metrics, users and inventory for Qiyun Asset Ops"
)]
struct Args {
    /// Configuration file. Created by --install.
    #[arg(long, default_value = "/etc/qiyun-agent.toml")]
    config: PathBuf,
    /// Override server URL from the config file.
    #[arg(long, env = "QIYUN_SERVER")]
    server: Option<String>,
    /// Override the Agent bearer token.
    #[arg(long, env = "QIYUN_AGENT_TOKEN")]
    token: Option<String>,
    /// Override collection interval in seconds.
    #[arg(long, env = "QIYUN_INTERVAL")]
    interval: Option<u64>,
    /// Override stale-user threshold in days.
    #[arg(long, env = "QIYUN_STALE_DAYS")]
    stale_days: Option<i64>,
    /// Override network interfaces, comma separated.
    #[arg(long, env = "QIYUN_NETWORK_INTERFACES")]
    network_interfaces: Option<String>,
    /// Install binary, config, systemd unit and enable startup; then exit.
    #[arg(long)]
    install: bool,
    /// Overwrite an existing config during --install.
    #[arg(long)]
    force_config: bool,
    /// Print dependency status and exit.
    #[arg(long)]
    check_deps: bool,
    /// Install missing dependencies and exit.
    #[arg(long)]
    install_deps: bool,
    /// Collect and upload once, then exit.
    #[arg(long)]
    once: bool,
    /// Print the JSON report to stdout and exit without uploading.
    #[arg(long)]
    print: bool,
    /// Accept self-signed TLS certificates (development only).
    #[arg(long)]
    insecure: bool,
}

fn main() {
    let args = Args::parse();
    tracing_subscriber::fmt()
        .with_writer(std::io::stderr)
        .with_env_filter(
            EnvFilter::try_from_default_env().unwrap_or_else(|_| EnvFilter::new("info")),
        )
        .with_target(false)
        .init();

    if let Err(error) = run(args) {
        tracing::error!("{error:#}");
        std::process::exit(1);
    }
}

fn load_config(args: &Args) -> Result<AgentConfig> {
    let mut config = AgentConfig::load(&args.config)?.unwrap_or_default();
    if let Some(value) = &args.server {
        config.server = value.clone();
    }
    if let Some(value) = &args.token {
        config.token = value.clone();
    }
    if let Some(value) = args.interval {
        config.interval = value;
    }
    if let Some(value) = args.stale_days {
        config.stale_days = value;
    }
    if let Some(value) = &args.network_interfaces {
        config.network_interfaces = split_list(value);
    }
    config.validate()?;
    Ok(config)
}

fn split_list(value: &str) -> Vec<String> {
    value
        .split([',', ' ', ';'])
        .map(str::trim)
        .filter(|item| !item.is_empty())
        .map(str::to_string)
        .collect()
}

fn run(args: Args) -> Result<()> {
    let config = load_config(&args)?;
    if args.check_deps {
        deps::print_checks(&config);
        return Ok(());
    }
    if args.install_deps {
        deps::ensure(&config, true)?;
        return Ok(());
    }
    if args.install {
        return install::run(install::Options {
            config_path: args.config.clone(),
            config,
            force_config: args.force_config,
            install_deps: false,
        });
    }
    if args.print {
        let report = collect::collect_report(&config);
        println!("{}", serde_json::to_string_pretty(&report)?);
        return Ok(());
    }
    if config.token.trim().is_empty() {
        bail!("缺少 Agent 令牌：在 {} 中设置 token，或传入 --token", args.config.display());
    }
    if config.token.contains('\r') || config.token.contains('\n') {
        bail!("Agent 令牌不能包含换行");
    }
    if args.once {
        let report = collect::collect_report(&config);
        upload(&config, args.insecure, &report)?;
        return Ok(());
    }

    let interval = config.interval.max(5);
    let mut failures = 0_u32;
    loop {
        let report = collect::collect_report(&config);
        match upload(&config, args.insecure, &report) {
            Ok(()) => failures = 0,
            Err(error) => {
                failures = failures.saturating_add(1);
                tracing::warn!(attempt = failures, "report upload failed: {error:#}");
            }
        }
        let backoff = interval
            .saturating_mul(1_u64 << failures.min(5))
            .min(300);
        std::thread::sleep(Duration::from_secs(backoff));
    }
}

fn upload(config: &AgentConfig, insecure: bool, report: &model::Report) -> Result<()> {
    let base = config.server.trim_end_matches('/');
    if base.is_empty() {
        bail!("server URL is empty");
    }
    let endpoint = format!("{base}/api/v1/agent/report");
    let payload = serde_json::to_vec(report)?;
    let dir = std::env::temp_dir().join(format!("qiyun-agent-upload-{}", std::process::id()));
    let mut builder = fs::DirBuilder::new();
    builder.recursive(true).mode(0o700).create(&dir)?;
    let payload_path = dir.join("payload.json");
    let header_path = dir.join("headers.txt");
    {
        let mut payload_file = OpenOptions::new()
            .create(true)
            .truncate(true)
            .write(true)
            .mode(0o600)
            .open(&payload_path)?;
        payload_file.write_all(&payload)?;
    }
    {
        let mut header = OpenOptions::new()
            .create(true)
            .truncate(true)
            .write(true)
            .mode(0o600)
            .open(&header_path)?;
        writeln!(header, "Authorization: Bearer {}", config.token.trim())?;
    }

    let mut curl_args = vec![
        "--silent".to_string(),
        "--show-error".to_string(),
        "--fail-with-body".to_string(),
        "--max-time".to_string(),
        "30".to_string(),
        "-X".to_string(),
        "POST".to_string(),
        "-H".to_string(),
        "Content-Type: application/json".to_string(),
        "-H".to_string(),
        format!("@{}", header_path.to_string_lossy()),
        "--data-binary".to_string(),
        format!("@{}", payload_path.to_string_lossy()),
    ];
    if insecure {
        curl_args.push("--insecure".to_string());
    }
    curl_args.push(endpoint);
    let refs: Vec<&str> = curl_args.iter().map(String::as_str).collect();
    let output = util::run("curl", &refs, Duration::from_secs(35))
        .context("调用 curl 上传报告失败；请确认已安装 curl")?;
    let _ = fs::remove_dir_all(&dir);
    if !output.success {
        bail!(
            "上传失败（curl 退出码 {}）：{}",
            output.code
                .map(|v| v.to_string())
                .unwrap_or_else(|| "unknown".to_string()),
            util::truncate(output.stderr.trim(), 300)
        );
    }
    tracing::info!(
        host_id = %report.host_id,
        hostname = %report.host.hostname,
        bytes = payload.len(),
        "inventory reported"
    );
    Ok(())
}
