//! whatevr's bundled assets: fonts now, icons and sounds later.

pub mod fonts {
    /// Instrument Sans v1.000, variable: wdth 75-100, wght 400-700. OFL 1.1.
    pub static INSTRUMENT_SANS: &[u8] = include_bytes!("../fonts/InstrumentSans[wdth,wght].ttf");
    pub static INSTRUMENT_SANS_ITALIC: &[u8] =
        include_bytes!("../fonts/InstrumentSans-Italic[wdth,wght].ttf");
    /// the family both files register as
    pub const FAMILY: &str = "Instrument Sans";
}
