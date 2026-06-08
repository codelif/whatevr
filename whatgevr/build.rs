fn main() {
    let protoc = protoc_bin_vendored::protoc_bin_path().expect("failed to find bundled protoc");
    unsafe {
        std::env::set_var("PROTOC", protoc);
    }

    let proto_file = std::env::var("WHATEVR_PROTO_FILE")
        .unwrap_or_else(|_| "../proto/whatevr.proto".to_string());
    let proto_include =
        std::env::var("WHATEVR_PROTO_INCLUDE").unwrap_or_else(|_| "../proto".to_string());

    tonic_build::configure()
        .build_server(false)
        .compile_protos(&[proto_file.as_str()], &[proto_include.as_str()])
        .expect("failed to compile protobuf definitions");

    println!("cargo:rerun-if-env-changed=WHATEVR_PROTO_FILE");
    println!("cargo:rerun-if-env-changed=WHATEVR_PROTO_INCLUDE");
    println!("cargo:rerun-if-changed={proto_file}");
}
