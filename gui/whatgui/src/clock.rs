//! local time for rows, in the system zone, read once at start.

use jiff::Timestamp;
use jiff::civil::Date;
use jiff::tz::TimeZone;
use log::warn;

const DAYS: [&str; 7] = [
    "Monday",
    "Tuesday",
    "Wednesday",
    "Thursday",
    "Friday",
    "Saturday",
    "Sunday",
];
const MONTHS: [&str; 12] = [
    "Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec",
];

#[derive(Clone)]
pub struct Clock {
    tz: TimeZone,
}

impl Clock {
    pub fn new() -> Self {
        let tz = TimeZone::try_system().unwrap_or_else(|e| {
            warn!("no system time zone, using utc: {e}");
            TimeZone::UTC
        });
        Self { tz }
    }

    fn date(&self, ms: i64) -> Date {
        self.at(ms).date()
    }

    fn at(&self, ms: i64) -> jiff::Zoned {
        Timestamp::from_millisecond(ms)
            .unwrap_or(Timestamp::UNIX_EPOCH)
            .to_zoned(self.tz.clone())
    }

    /// the local day ms falls on, days since 1970-01-01
    pub fn day(&self, ms: i64) -> i32 {
        days(self.date(ms))
    }

    pub fn today(&self) -> i32 {
        days(Timestamp::now().to_zoned(self.tz.clone()).date())
    }

    /// 24 hour "14:05"
    pub fn hm(&self, ms: i64) -> String {
        let z = self.at(ms);
        format!("{:02}:{:02}", z.hour(), z.minute())
    }

    /// a day row's weekday and date, "Monday" and "3 Nov 2026"
    pub fn day_row(&self, ms: i64) -> (String, String) {
        let d = self.date(ms);
        (
            DAYS[d.weekday().to_monday_zero_offset() as usize].to_string(),
            format!(
                "{} {} {}",
                d.day(),
                MONTHS[d.month() as usize - 1],
                d.year()
            ),
        )
    }

    /// the chat list's time: today's clock, Yesterday, the weekday this
    /// week, else dd/mm/yy
    pub fn list(&self, ms: i64, today: i32) -> String {
        if ms <= 0 {
            return String::new();
        }
        let d = self.date(ms);
        match today - days(d) {
            ..=0 => self.hm(ms),
            1 => "Yesterday".into(),
            2..=6 => DAYS[d.weekday().to_monday_zero_offset() as usize][..3].into(),
            _ => format!("{:02}/{:02}/{:02}", d.day(), d.month(), d.year() % 100),
        }
    }
}

/// days since 1970-01-01 of a civil date, Hinnant's days_from_civil
fn days(d: Date) -> i32 {
    let (y, m, dd) = (d.year() as i32, d.month() as i32, d.day() as i32);
    let y = if m <= 2 { y - 1 } else { y };
    let era = y.div_euclid(400);
    let yoe = y - era * 400;
    let doy = (153 * (m + if m > 2 { -3 } else { 9 }) + 2) / 5 + dd - 1;
    let doe = yoe * 365 + yoe / 4 - yoe / 100 + doy;
    era * 146_097 + doe - 719_468
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn days_and_labels() {
        assert_eq!(days(jiff::civil::date(1970, 1, 1)), 0);
        assert_eq!(days(jiff::civil::date(2026, 11, 1)), 20_758);
        let c = Clock { tz: TimeZone::UTC };
        // 2026-11-01 13:05 utc, a sunday
        let ms = 1_793_538_300_000;
        assert_eq!(c.hm(ms), "13:05");
        assert_eq!(c.day_row(ms), ("Sunday".into(), "1 Nov 2026".into()));
        let today = c.day(ms);
        assert_eq!(c.list(ms, today), "13:05");
        assert_eq!(c.list(ms, today + 1), "Yesterday");
        assert_eq!(c.list(ms, today + 3), "Sun");
        assert_eq!(c.list(ms, today + 7), "01/11/26");
    }
}
