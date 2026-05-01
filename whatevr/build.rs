fn main() {
    let protoc = protoc_bin_vendored::protoc_bin_path().expect("failed to find bundled protoc");
    unsafe {
        std::env::set_var("PROTOC", protoc);
    }

    tonic_build::configure()
        .build_server(false)
        .compile_protos(&["../whatevrd/proto/whatevr.proto"], &["../whatevrd/proto"])
        .expect("failed to compile protobuf definitions");

    println!("cargo:rerun-if-changed=../whatevrd/proto/whatevr.proto");
}
