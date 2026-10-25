pub mod docker;
pub mod host;
pub mod k8s;
pub mod logs;
pub mod metrics;
pub mod network;
pub mod pve;
pub mod users;

use crate::config::AgentConfig;
use crate::model::{Platforms, Report};
use crate::util::{host_id, now_rfc3339};
use std::time::Instant;

pub fn collect_report(config: &AgentConfig) -> Report {
    let started = Instant::now();
    let host = host::collect(&config.network_spec(), &config.metrics);
    let users = users::collect(config.stale_days, &config.audit);
    let pve = pve::collect();
    let docker = docker::collect();
    let kubernetes = k8s::collect();
    tracing::info!(
        hostname = %host.hostname,
        pve = pve.available,
        docker = docker.available,
        kubernetes = kubernetes.available,
        elapsed_ms = started.elapsed().as_millis(),
        "inventory collected"
    );
    Report {
        schema_version: 1,
        agent_version: env!("CARGO_PKG_VERSION").to_string(),
        host_id: if config.host_id.trim().is_empty() {
            host_id()
        } else {
            config.host_id.trim().to_string()
        },
        collected_at: now_rfc3339(),
        host,
        users,
        platforms: Platforms {
            pve,
            docker,
            kubernetes,
        },
        logs: logs::collect(&config.logs),
    }
}
