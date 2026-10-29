//! deterministic fake data: one busy group chat, 100k rows of every kind
//! the bubbles draw, and 5k chats for the sidebar.

use std::sync::Arc;

use what_ui::{Dir, ListSource};

/// splitmix64
#[derive(Clone)]
pub struct Rng(u64);

impl Rng {
    pub fn new(seed: u64) -> Self {
        Self(seed)
    }

    pub fn u64(&mut self) -> u64 {
        self.0 = self.0.wrapping_add(0x9e37_79b9_7f4a_7c15);
        let mut z = self.0;
        z = (z ^ (z >> 30)).wrapping_mul(0xbf58_476d_1ce4_e5b9);
        z = (z ^ (z >> 27)).wrapping_mul(0x94d0_49bb_1331_11eb);
        z ^ (z >> 31)
    }

    /// 0..1
    pub fn f(&mut self) -> f32 {
        (self.u64() >> 40) as f32 / (1u64 << 24) as f32
    }

    pub fn below(&mut self, n: usize) -> usize {
        (self.u64() % n as u64) as usize
    }

    pub fn pick<'a, T>(&mut self, v: &'a [T]) -> &'a T {
        &v[self.below(v.len())]
    }

    pub fn chance(&mut self, p: f32) -> bool {
        self.f() < p
    }
}

pub struct Person {
    pub name: &'static str,
    /// into the theme's name colours
    pub color: u8,
}

pub const PEOPLE: [Person; 6] = [
    Person {
        name: "Meera Iyer",
        color: 0,
    },
    Person {
        name: "Kenji Sato",
        color: 1,
    },
    Person {
        name: "Omar Haddad",
        color: 2,
    },
    Person {
        name: "Lena Vogel",
        color: 3,
    },
    Person {
        name: "Ana Lucía Ferreira",
        color: 4,
    },
    Person {
        name: "Jonas",
        color: 0,
    },
];

pub fn initials(name: &str) -> String {
    name.split_whitespace()
        .filter_map(|w| w.chars().next())
        .take(2)
        .collect()
}

#[derive(Clone)]
pub enum Body {
    Text,
    /// width / height
    Photo {
        ratio: f32,
        hue: f32,
    },
    Link {
        title: String,
        host: String,
        hue: f32,
        /// the site's image is wide: big preview, else a side thumb
        wide: bool,
    },
}

#[derive(Clone)]
pub struct Quote {
    /// None is you
    pub from: Option<u8>,
    pub text: String,
}

#[derive(Clone)]
pub struct Msg {
    /// into PEOPLE; None is you
    pub from: Option<u8>,
    /// the caption for a photo
    pub text: String,
    pub body: Body,
    pub quote: Option<Quote>,
    pub reactions: Vec<(&'static str, u32)>,
    pub time: String,
    /// 1 sent, 2 delivered, 3 read
    pub state: u8,
    /// first and last of a run from one sender
    pub first: bool,
    pub last: bool,
}

pub enum Entry {
    Day { day: String, date: String },
    System(String),
    Msg(Msg),
}

/// what a chat row is built from
#[derive(Clone)]
pub enum Item {
    Entry(Arc<Entry>),
    /// the chat's top padding, above the very first message
    Top,
    /// the chat's bottom padding, under the newest message
    Bottom,
}

pub const TOP: u32 = u32::MAX - 1;
pub const BOTTOM: u32 = u32::MAX;

const SHORT: &[&str] = &[
    "ok",
    "haha",
    "👍",
    "on my way",
    "sure, see you there",
    "lol 😂",
    "nope",
    "yes!!",
    "wait what",
    "brb",
    "done ✅",
    "🙏🙏",
    "can't today, sorry",
    "kal milte hain",
    "ठीक है",
    "了解です",
    "好的",
    "حسنا",
    "давай",
    "알겠어",
    "ok 👌🏽",
    "🎉🎉🎉",
    "same",
    "true",
    "omw, 5 min",
];

const LINES: &[&str] = &[
    "Did anyone check if the place is open on Sunday? Last time we went it was closed and we ended up eating at the station.",
    "कल शाम को मिलते हैं, ठीक है? मैं सात बजे तक पहुँच जाऊँगी 🙂",
    "明天早上九点见，别迟到了 😄 我会带上那本书和上次说的地图",
    "سأصل بعد عشر دقائق تقريباً، الطريق مزدحم جداً اليوم",
    "Мы уже на месте, заходите через главный вход",
    "今日は雨が降りそうだから、傘を持ってきてね ☔",
    "I booked the tickets for the 18:40 train, seats 23 to 26 in coach C. Send me your share whenever.",
    "okay so the plan: meet at 10, breakfast at that café near the bridge, then the museum, then we figure it out 🗺️",
    "내일 몇 시에 만날까? 나는 오후 두 시 이후로는 다 괜찮아",
    "Η συνάντηση μεταφέρθηκε για την Πέμπτη",
    "can someone bring a charger, mine died and I have like 4% left 🔋",
    "जी हाँ, मैंने टिकट बुक कर दिए हैं। सबको मेल भी भेज दिया है।",
    "ฉันจะไปถึงประมาณบ่ายสามโมงนะ",
    "photos from yesterday are up in the shared album, the ones from the boat came out really well 📸",
    "😂😂😂 I can't believe he actually did it",
    "Quick question: is it 12 € per person or for the whole table? The menu wasn't clear about it.",
    "שבת שלום! מתי אתם מגיעים?",
    "pls don't forget the cake 🎂 it's in the second fridge, top shelf",
    "We could also take the ferry instead, it's slower but way nicer and it's only 20 minutes more.",
    "مرحبا بالجميع 👋 هل الموعد ما زال الساعة الثامنة؟",
];

const LONG: &str = "Long one, sorry. I talked to the owner and he said we can have the back room from seven, but only if we confirm the number of people by Thursday and pay half up front. He also said the kitchen closes at ten, so if anyone is coming late they should order before that. I think it's a good deal compared to the other places we looked at, and it's close to the station for the people coming from out of town. Let me know by tomorrow if you're in, and if you're bringing someone, so I can give him a final number. 🙏";

const HOSTS: &[&str] = &[
    "example.org",
    "maps.example.com",
    "news.example.net",
    "blog.example.io",
    "tickets.example.com",
];

const TITLES: &[&str] = &[
    "Haveli: rooftop dining in the old city, open till 23:30",
    "Ferry timetable, winter schedule",
    "The 12 best day trips from the city, ranked by someone who did them all",
    "Booking confirmed: 4 seats, coach C",
    "Weather: rain likely after 15:00",
];

const SYSTEM: &[&str] = &[
    "Lena added Omar",
    "Kenji changed the group name to “Weekend plans”",
    "Meera changed this group's icon",
    "Ana Lucía left",
    "Jonas joined using this group's invite link",
];

const REACTIONS: &[&str] = &["👍", "❤️", "😂", "😮", "🙏", "🎉"];

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
    "January",
    "February",
    "March",
    "April",
    "May",
    "June",
    "July",
    "August",
    "September",
    "October",
    "November",
    "December",
];

/// days since 1970-01-01 to (year, month 0.., day 1..)
fn civil(days: i64) -> (i64, usize, u32) {
    // howard hinnant's days_from_civil, backwards
    let z = days + 719_468;
    let era = z.div_euclid(146_097);
    let doe = z - era * 146_097;
    let yoe = (doe - doe / 1460 + doe / 36_524 - doe / 146_096) / 365;
    let doy = doe - (365 * yoe + yoe / 4 - yoe / 100);
    let mp = (5 * doy + 2) / 153;
    let d = (doy - (153 * mp + 2) / 5 + 1) as u32;
    let m = if mp < 10 { mp + 3 } else { mp - 9 };
    let y = yoe + era * 400 + if m <= 2 { 1 } else { 0 };
    (y, (m - 1) as usize, d)
}

/// makes the chat in order, oldest first, so runs and days come out right
struct Gen {
    rng: Rng,
    /// unix minutes
    clock: i64,
    last_day: i64,
    last_from: Option<Option<u8>>,
}

impl Gen {
    fn text(&mut self) -> String {
        let r = self.rng.f();
        if r < 0.45 {
            self.rng.pick(SHORT).to_string()
        } else if r < 0.93 {
            self.rng.pick(LINES).to_string()
        } else if r < 0.97 {
            let a = self.rng.pick(LINES);
            let b = self.rng.pick(LINES);
            format!("{a}\n{b}")
        } else {
            LONG.to_string()
        }
    }

    fn from(&mut self) -> Option<u8> {
        if let Some(f) = self.last_from
            && self.rng.chance(0.55)
        {
            return f;
        }
        if self.rng.chance(0.3) {
            None
        } else {
            Some(self.rng.below(PEOPLE.len()) as u8)
        }
    }

    /// the next entries: a day row first when the day turns
    fn next(&mut self, out: &mut Vec<Entry>) {
        let gap = if self.rng.chance(0.6) {
            self.rng.below(4) as i64
        } else {
            // mostly minutes, sometimes hours
            (-(1.0 - self.rng.f()).ln() * 90.0) as i64 + 1
        };
        self.clock += gap;
        let day = self.clock.div_euclid(1440);
        if day != self.last_day {
            self.last_day = day;
            self.last_from = None;
            let (y, m, d) = civil(day);
            out.push(Entry::Day {
                day: DAYS[(day + 3).rem_euclid(7) as usize].to_string(),
                date: format!("{d} {} {y}", MONTHS[m]),
            });
        }
        if self.rng.chance(0.004) {
            self.last_from = None;
            out.push(Entry::System(self.rng.pick(SYSTEM).to_string()));
            return;
        }
        let from = self.from();
        let r = self.rng.f();
        let (body, text) = if r < 0.08 {
            let ratio = *self.rng.pick(&[0.75f32, 1.0, 1.333, 1.5, 0.5625]);
            let body = Body::Photo {
                ratio,
                hue: self.rng.f() * 360.0,
            };
            let caption = if self.rng.chance(0.5) {
                self.text()
            } else {
                String::new()
            };
            (body, caption)
        } else if r < 0.13 {
            let host = self.rng.pick(HOSTS).to_string();
            let path = self.rng.below(9000) + 1000;
            let text = if self.rng.chance(0.5) {
                format!("https://{host}/p/{path}")
            } else {
                format!("look at this https://{host}/p/{path} 👀")
            };
            let body = Body::Link {
                title: self.rng.pick(TITLES).to_string(),
                host,
                hue: self.rng.f() * 360.0,
                wide: self.rng.chance(0.5),
            };
            (body, text)
        } else {
            (Body::Text, self.text())
        };
        let quote = self.rng.chance(0.1).then(|| Quote {
            from: if self.rng.chance(0.3) {
                None
            } else {
                Some(self.rng.below(PEOPLE.len()) as u8)
            },
            text: self.text(),
        });
        let reactions = if self.rng.chance(0.12) {
            let n = 1 + self.rng.below(3);
            let mut v: Vec<(&'static str, u32)> = Vec::new();
            for _ in 0..n {
                let e = *self.rng.pick(REACTIONS);
                if !v.iter().any(|(x, _)| *x == e) {
                    v.push((e, 1 + self.rng.below(4) as u32));
                }
            }
            v
        } else {
            Vec::new()
        };
        let first = self.last_from != Some(from);
        self.last_from = Some(from);
        let m = self.clock.rem_euclid(1440);
        out.push(Entry::Msg(Msg {
            from,
            text,
            body,
            quote,
            reactions,
            time: format!("{:02}:{:02}", m / 60, m % 60),
            state: 3,
            first,
            last: true,
        }));
    }
}

/// a run's last flag is only known once the next entry is
fn close_runs(v: &mut [Entry]) {
    for i in 1..v.len() {
        let (a, b) = v.split_at_mut(i);
        if let (Entry::Msg(prev), Entry::Msg(next)) = (&mut a[i - 1], &b[0]) {
            prev.last = next.first;
        }
    }
}

pub struct Chat {
    entries: Vec<Arc<Entry>>,
    revs: Vec<u64>,
    /// first loaded entry; older ones arrive through prepend
    lo: usize,
    generator: Gen,
}

impl Chat {
    /// `history` entries made up front, the oldest `unloaded` of them held
    /// back for prepends
    pub fn new(seed: u64, history: usize, unloaded: usize) -> Self {
        let mut generator = Gen {
            rng: Rng::new(seed),
            // 2021-03-01
            clock: 18_687 * 1440 + 8 * 60,
            last_day: 0,
            last_from: None,
        };
        let mut v = Vec::with_capacity(history + 64);
        while v.len() < history {
            generator.next(&mut v);
        }
        close_runs(&mut v);
        // the newest few aren't read yet
        let mut own = 0;
        for e in v.iter_mut().rev() {
            if let Entry::Msg(m) = e
                && m.from.is_none()
            {
                m.state = [1, 2, 2][own.min(2)];
                own += 1;
                if own == 3 {
                    break;
                }
            }
        }
        let n = v.len();
        Self {
            entries: v.into_iter().map(Arc::new).collect(),
            revs: vec![0; n],
            lo: unloaded.min(n),
            generator,
        }
    }

    pub fn len(&self) -> usize {
        self.entries.len() - self.lo
    }

    pub fn is_empty(&self) -> bool {
        self.len() == 0
    }

    pub fn oldest_loaded(&self) -> Option<u32> {
        (self.lo < self.entries.len()).then_some(self.lo as u32)
    }

    pub fn has_more(&self) -> bool {
        self.lo > 0
    }

    /// load up to `n` older entries; how many came
    pub fn prepend(&mut self, n: usize) -> usize {
        let got = n.min(self.lo);
        self.lo -= got;
        got
    }

    /// a new message from someone else (or you); returns the new key
    pub fn incoming(&mut self) -> u32 {
        let start = self.entries.len();
        let mut fresh = Vec::new();
        while fresh.is_empty() || !matches!(fresh.last(), Some(Entry::Msg(_))) {
            self.generator.next(&mut fresh);
        }
        close_runs(&mut fresh);
        let first_new = fresh.iter().find_map(|e| match e {
            Entry::Msg(m) => Some(m.first),
            _ => None,
        });
        let joins = matches!(fresh.first(), Some(Entry::Msg(_))) && first_new == Some(false);
        // the old newest message stops being the end of its run
        if joins && let Some(Entry::Msg(prev)) = self.entries.last().map(|e| e.as_ref()) {
            let i = start - 1;
            let mut m = prev.clone();
            m.last = false;
            self.entries[i] = Arc::new(Entry::Msg(m));
            self.revs[i] += 1;
        }
        for mut e in fresh {
            if let Entry::Msg(m) = &mut e
                && m.from.is_none()
            {
                m.state = 1;
            }
            self.entries.push(Arc::new(e));
            self.revs.push(0);
        }
        (self.entries.len() - 1) as u32
    }

    pub fn get(&self, k: u32) -> Option<&Entry> {
        self.entries.get(k as usize).map(|e| e.as_ref())
    }
}

impl ListSource for Chat {
    type Key = u32;
    type Content = Item;

    fn newest(&self) -> Option<u32> {
        Some(BOTTOM)
    }

    fn neighbour(&self, k: u32, dir: Dir) -> Option<u32> {
        let n = self.entries.len();
        let last = (n > self.lo).then(|| (n - 1) as u32);
        match (k, dir) {
            (BOTTOM, Dir::Older) => last.or((self.lo == 0).then_some(TOP)),
            (BOTTOM, Dir::Newer) | (TOP, Dir::Older) => None,
            (TOP, Dir::Newer) => last.map(|_| self.lo as u32).or(Some(BOTTOM)),
            (k, Dir::Older) if k as usize == self.lo => (self.lo == 0).then_some(TOP),
            (k, Dir::Older) => Some(k - 1),
            (k, Dir::Newer) if Some(k) == last => Some(BOTTOM),
            (k, Dir::Newer) => Some(k + 1),
        }
    }

    fn revision(&self, k: u32) -> u64 {
        self.revs.get(k as usize).copied().unwrap_or(0)
    }

    fn content(&self, k: u32) -> Item {
        match k {
            TOP => Item::Top,
            BOTTOM => Item::Bottom,
            k => Item::Entry(self.entries[k as usize].clone()),
        }
    }

    fn fraction(&self, k: u32) -> f32 {
        match k {
            TOP => 0.0,
            BOTTOM => 1.0,
            k => {
                let n = self.len().max(2) - 1;
                (k as usize - self.lo) as f32 / n as f32
            }
        }
    }

    fn at_fraction(&self, f: f32) -> Option<u32> {
        if self.is_empty() {
            return Some(BOTTOM);
        }
        let n = self.len() - 1;
        Some((self.lo + (f.clamp(0.0, 1.0) * n as f32).round() as usize) as u32)
    }

    fn total(&self) -> Option<usize> {
        None
    }
}

/// one chat in the sidebar
pub struct ChatInfo {
    pub name: String,
    pub group: bool,
    pub color: u8,
    /// None: they sent it (or nobody in a group, a 1:1); Some("") is you
    pub from: Option<String>,
    pub preview: String,
    /// your last message's ticks, when `from` is you
    pub state: u8,
    pub time: String,
    pub unread: u32,
}

const FIRST: &[&str] = &[
    "Meera",
    "Kenji",
    "Omar",
    "Lena",
    "Ana Lucía",
    "Jonas",
    "Priya",
    "Yuki",
    "Fatima",
    "Mateo",
    "Sofia",
    "Arjun",
    "Hana",
    "Youssef",
    "Elena",
    "Wei",
    "Aarav",
    "Chloé",
    "Dmitri",
    "Ines",
];

const LAST: &[&str] = &[
    "Iyer",
    "Sato",
    "Haddad",
    "Vogel",
    "Ferreira dos Santos",
    "Nakamura",
    "Sharma",
    "Rossi",
    "Kowalski",
    "Okafor",
    "Lindqvist",
    "Moreau",
];

const GROUPS: &[&str] = &[
    "Weekend plans",
    "Flat 4B",
    "Family",
    "Book club 📚",
    "Office lunch",
    "Trip to the mountains 🏔️",
    "Football Thursdays",
    "Class of 2014",
    "Neighbours",
    "Band practice",
];

pub struct Chats {
    pub chats: Vec<Arc<ChatInfo>>,
}

impl Chats {
    pub fn new(seed: u64, n: usize) -> Self {
        let mut rng = Rng::new(seed);
        let mut chats = Vec::with_capacity(n);
        // newest first: times walk back from now
        let mut minutes = 0u32;
        for i in 0..n {
            minutes += rng.below(if i < 20 { 40 } else { 600 }) as u32 + 1;
            let group = rng.chance(0.25);
            let name = if group {
                let g = rng.pick(GROUPS);
                if i < GROUPS.len() * 2 {
                    g.to_string()
                } else {
                    format!("{g} {}", 2 + rng.below(30))
                }
            } else {
                format!("{} {}", rng.pick(FIRST), rng.pick(LAST))
            };
            let from = if rng.chance(0.35) {
                Some(String::new())
            } else if group {
                Some(rng.pick(FIRST).to_string())
            } else {
                None
            };
            let preview = if rng.chance(0.08) {
                "📷 Photo".to_string()
            } else if rng.chance(0.5) {
                rng.pick(LINES).to_string()
            } else {
                rng.pick(SHORT).to_string()
            };
            let unread = if from.as_deref() == Some("") || !rng.chance(0.3) {
                0
            } else if rng.chance(0.08) {
                100 + rng.below(400) as u32
            } else {
                1 + rng.below(20) as u32
            };
            let time = if minutes < 600 {
                let m = (600 - minutes) % 1440 + 8 * 60;
                format!("{:02}:{:02}", m / 60 % 24, m % 60)
            } else if minutes < 1440 * 2 {
                "Yesterday".to_string()
            } else if minutes < 1440 * 7 {
                DAYS[(minutes / 1440) as usize % 7][..3].to_string()
            } else {
                let (y, m, d) = civil(20_390 - (minutes / 1440) as i64);
                format!("{d:02}/{:02}/{:02}", m + 1, y % 100)
            };
            chats.push(Arc::new(ChatInfo {
                name,
                group,
                color: rng.below(5) as u8,
                from,
                preview,
                state: *rng.pick(&[1, 2, 3, 3, 3]),
                time,
                unread,
            }));
        }
        Self { chats }
    }
}

impl ListSource for Chats {
    type Key = u32;
    type Content = Arc<ChatInfo>;

    fn newest(&self) -> Option<u32> {
        self.chats.len().checked_sub(1).map(|n| n as u32)
    }

    fn neighbour(&self, k: u32, dir: Dir) -> Option<u32> {
        match dir {
            Dir::Older => k.checked_sub(1),
            Dir::Newer => ((k as usize + 1) < self.chats.len()).then_some(k + 1),
        }
    }

    fn revision(&self, _: u32) -> u64 {
        0
    }

    fn content(&self, k: u32) -> Arc<ChatInfo> {
        self.chats[k as usize].clone()
    }

    fn fraction(&self, k: u32) -> f32 {
        k as f32 / (self.chats.len().max(2) - 1) as f32
    }

    fn at_fraction(&self, f: f32) -> Option<u32> {
        let n = self.newest()?;
        Some((f.clamp(0.0, 1.0) * n as f32).round() as u32)
    }

    fn total(&self) -> Option<usize> {
        None
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn same_seed_same_chat() {
        let a = Chat::new(7, 2000, 500);
        let b = Chat::new(7, 2000, 500);
        let text = |c: &Chat, k| match c.get(k) {
            Some(Entry::Msg(m)) => m.text.clone(),
            Some(Entry::Day { date, .. }) => date.clone(),
            Some(Entry::System(s)) => s.clone(),
            None => String::new(),
        };
        for k in [0, 500, 1999] {
            assert_eq!(text(&a, k), text(&b, k));
        }
    }

    #[test]
    fn walks_end_to_end_through_the_pads() {
        let mut c = Chat::new(1, 300, 100);
        assert_eq!(
            c.neighbour(BOTTOM, Dir::Older),
            Some(c.entries.len() as u32 - 1)
        );
        assert_eq!(c.neighbour(100, Dir::Older), None);
        c.prepend(1000);
        assert_eq!(c.neighbour(0, Dir::Older), Some(TOP));
        let mut k = TOP;
        let mut n = 0;
        while let Some(next) = c.neighbour(k, Dir::Newer) {
            k = next;
            n += 1;
        }
        assert_eq!((k, n), (BOTTOM, c.entries.len() + 1));
    }

    #[test]
    fn runs_join_across_incoming() {
        let mut c = Chat::new(3, 50, 0);
        for _ in 0..200 {
            let k = c.incoming();
            if let (Some(Entry::Msg(prev)), Some(Entry::Msg(new))) = (c.get(k - 1), c.get(k)) {
                assert_eq!(prev.last, new.first);
            }
        }
    }
}
