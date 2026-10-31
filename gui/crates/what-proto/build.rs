use std::path::Path;

fn main() {
    let root = Path::new(env!("CARGO_MANIFEST_DIR")).join("../../../proto");
    let dir = root.join("whatevr/v2");
    println!("cargo:rerun-if-changed={}", dir.display());
    let mut files: Vec<_> = std::fs::read_dir(&dir)
        .expect("proto/whatevr/v2")
        .map(|e| e.expect("proto entry").path())
        .filter(|p| p.extension().is_some_and(|x| x == "proto"))
        .collect();
    files.sort();
    for f in &files {
        println!("cargo:rerun-if-changed={}", f.display());
    }
    buffa_build::Config::new()
        .files(&files)
        .includes(&[root])
        .generate_views(true)
        // whatgui never sends a received row back, so nothing to round trip
        .preserve_unknown_fields(false)
        .compile()
        .expect("protoc + buffa codegen");
}
