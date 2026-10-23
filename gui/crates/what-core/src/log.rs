//! tiny stderr logger behind the `log` facade.
//! spec: `debug` or `info,what_platform=trace,what_render::surface=debug`

use std::io::Write;
use std::str::FromStr;
use std::sync::OnceLock;
use std::time::Instant;

use log::{LevelFilter, Log, Metadata, Record};

struct Logger {
    default: LevelFilter,
    rules: Vec<(String, LevelFilter)>,
    start: Instant,
}

static LOGGER: OnceLock<Logger> = OnceLock::new();

/// reads the spec from `env_var`; unset or empty means `default`. call once.
pub fn init(env_var: &str, default: LevelFilter) {
    let spec = std::env::var(env_var).unwrap_or_default();
    let logger = LOGGER.get_or_init(|| parse(&spec, default));
    let max = logger
        .rules
        .iter()
        .map(|(_, l)| *l)
        .fold(logger.default, Ord::max);
    if log::set_logger(logger).is_ok() {
        log::set_max_level(max);
    }
}

fn parse(spec: &str, default: LevelFilter) -> Logger {
    let mut logger = Logger {
        default,
        rules: Vec::new(),
        start: Instant::now(),
    };
    for item in spec.split(',').map(str::trim).filter(|s| !s.is_empty()) {
        match item.split_once('=') {
            Some((target, level)) => match LevelFilter::from_str(level.trim()) {
                Ok(l) => logger.rules.push((target.trim().replace('-', "_"), l)),
                Err(_) => eprintln!("log: bad level in {item:?}, ignored"),
            },
            None => match LevelFilter::from_str(item) {
                Ok(l) => logger.default = l,
                Err(_) => eprintln!("log: bad level {item:?}, ignored"),
            },
        }
    }
    // longest prefix first so the most specific rule wins
    logger.rules.sort_by_key(|r| std::cmp::Reverse(r.0.len()));
    logger
}

impl Logger {
    fn level_for(&self, target: &str) -> LevelFilter {
        self.rules
            .iter()
            .find(|(prefix, _)| {
                target == prefix
                    || (target.starts_with(prefix.as_str())
                        && target[prefix.len()..].starts_with("::"))
            })
            .map_or(self.default, |(_, l)| *l)
    }
}

impl Log for Logger {
    fn enabled(&self, meta: &Metadata<'_>) -> bool {
        meta.level() <= self.level_for(meta.target())
    }

    fn log(&self, record: &Record<'_>) {
        if !self.enabled(record.metadata()) {
            return;
        }
        let t = self.start.elapsed().as_secs_f64();
        let _ = writeln!(
            std::io::stderr().lock(),
            "{t:>10.6} {:<5} {}: {}",
            record.level(),
            record.target(),
            record.args()
        );
    }

    fn flush(&self) {}
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn most_specific_rule_wins() {
        let l = parse(
            "warn, what_platform=debug ,what_platform::keyboard=trace",
            LevelFilter::Info,
        );
        assert_eq!(l.level_for("whatgui"), LevelFilter::Warn);
        assert_eq!(l.level_for("what_platform::window"), LevelFilter::Debug);
        assert_eq!(
            l.level_for("what_platform::keyboard::xkb"),
            LevelFilter::Trace
        );
        assert_eq!(l.level_for("what_platformx"), LevelFilter::Warn);
    }

    #[test]
    fn empty_spec_uses_default() {
        assert_eq!(
            parse("", LevelFilter::Info).level_for("x"),
            LevelFilter::Info
        );
    }
}
