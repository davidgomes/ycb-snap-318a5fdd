//! Tests for Wasm coredump generation upon Wasm traps.

use wasmi::{Caller, Config, Engine, Error, Extern, Func, Linker, Module, Store};

struct Reader<'a>(&'a [u8]);

impl Reader<'_> {
    fn byte(&mut self) -> u8 {
        let (first, rest) = self.0.split_first().unwrap();
        self.0 = rest;
        *first
    }

    fn uleb(&mut self) -> u64 {
        let (mut result, mut shift) = (0, 0);
        loop {
            let byte = self.byte();
            result |= u64::from(byte & 0x7F) << shift;
            shift += 7;
            if byte & 0x80 == 0 {
                return result;
            }
        }
    }

    fn sleb(&mut self) -> i64 {
        let (mut result, mut shift) = (0_i64, 0);
        loop {
            let byte = self.byte();
            result |= i64::from(byte & 0x7F) << shift;
            shift += 7;
            if byte & 0x80 == 0 {
                if shift < 64 && byte & 0x40 != 0 {
                    result |= -1 << shift;
                }
                return result;
            }
        }
    }

    fn bytes(&mut self, len: usize) -> &[u8] {
        let (head, rest) = self.0.split_at(len);
        self.0 = rest;
        head
    }

    fn name(&mut self) -> String {
        let len = self.uleb() as usize;
        String::from_utf8(self.bytes(len).to_vec()).unwrap()
    }
}

#[derive(Debug, PartialEq)]
struct Frame {
    instance: u64,
    func: u64,
    locals: Vec<i64>,
}

/// Returns the sections of the coredump as `(id, custom name, payload)`.
fn sections(dump: &[u8]) -> Vec<(u8, String, Vec<u8>)> {
    assert_eq!(&dump[..8], b"\0asm\x01\0\0\0");
    let mut reader = Reader(&dump[8..]);
    let mut sections = Vec::new();
    while !reader.0.is_empty() {
        let id = reader.byte();
        let len = reader.uleb() as usize;
        let mut payload = Reader(reader.bytes(len));
        let name = if id == 0 {
            payload.name()
        } else {
            String::new()
        };
        sections.push((id, name, payload.0.to_vec()));
    }
    sections
}

fn custom<'a>(sections: &'a [(u8, String, Vec<u8>)], name: &str) -> &'a [u8] {
    &sections
        .iter()
        .find(|(id, n, _)| *id == 0 && n == name)
        .unwrap()
        .2
}

fn frames(dump: &[u8]) -> Vec<Frame> {
    let sections = sections(dump);
    let mut reader = Reader(custom(&sections, "corestack"));
    assert_eq!(reader.byte(), 0x00);
    reader.name();
    let len = reader.uleb();
    (0..len)
        .map(|_| {
            assert_eq!(reader.byte(), 0x00);
            let instance = reader.uleb();
            let func = reader.uleb();
            let _offset = reader.uleb();
            let locals = (0..reader.uleb())
                .map(|_| match reader.byte() {
                    0x7F | 0x7E => reader.sleb(),
                    tag => panic!("unexpected value tag: {tag:#x}"),
                })
                .collect();
            assert_eq!(reader.uleb(), 0);
            Frame {
                instance,
                func,
                locals,
            }
        })
        .collect()
}

fn engine(enable: bool) -> Engine {
    let mut config = Config::default();
    config.generate_coredump(enable);
    config.coredump_executable_name("my-exe");
    Engine::new(&config)
}

const WASM: &str = r#"
    (module
        (import "env" "host" (func $host (param i32) (result i32)))
        (memory (export "mem") 1 2)
        (global $g (mut i32) (i32.const 42))
        (data (i32.const 0) "\01\02\03")
        (func (export "outer") (param i32) (result i32)
            (local i64)
            (local.set 1 (i64.const -7))
            (call $host (local.get 0))
        )
        (func (export "inner") (param i32) (result i32)
            (call $trap (i32.add (local.get 0) (i32.const 1)))
        )
        (func $trap (param i32) (result i32)
            (unreachable)
        )
    )
"#;

fn run(enable: bool) -> Error {
    let engine = engine(enable);
    let mut store = Store::new(&engine, ());
    let mut linker = <Linker<()>>::new(&engine);
    let host = Func::wrap(
        &mut store,
        |mut caller: Caller<()>, input: i32| -> Result<i32, Error> {
            let inner = caller
                .get_export("inner")
                .and_then(Extern::into_func)
                .unwrap()
                .typed::<i32, i32>(&caller)
                .unwrap();
            inner.call(&mut caller, input * 2)
        },
    );
    linker.define("env", "host", host).unwrap();
    let module = Module::new(&engine, WASM).unwrap();
    let instance = linker.instantiate_and_start(&mut store, &module).unwrap();
    let outer = instance
        .get_typed_func::<i32, i32>(&store, "outer")
        .unwrap();
    outer.call(&mut store, 5).unwrap_err()
}

#[test]
fn coredump_disabled_by_default() {
    assert!(run(false).coredump().is_none());
}

#[test]
fn coredump_reentrant_trap() {
    let error = run(true);
    let dump = error.coredump().expect("missing coredump");
    assert_eq!(
        frames(dump),
        vec![
            Frame {
                instance: 0,
                func: 3,
                locals: vec![11]
            },
            Frame {
                instance: 0,
                func: 2,
                locals: vec![10]
            },
            Frame {
                instance: 0,
                func: 1,
                locals: vec![5, -7]
            },
        ]
    );
    let sections = sections(dump);
    let mut core = Reader(custom(&sections, "core"));
    assert_eq!(core.byte(), 0x00);
    assert_eq!(core.name(), "my-exe");
    let mut instances = Reader(custom(&sections, "coreinstances"));
    assert_eq!(instances.uleb(), 1);
    assert_eq!(instances.byte(), 0x00);
    assert_eq!(instances.uleb(), 0);
    assert_eq!((instances.uleb(), instances.uleb()), (1, 0));
    assert_eq!((instances.uleb(), instances.uleb()), (1, 0));
    let memory = &sections.iter().find(|s| s.0 == 5).unwrap().2;
    assert_eq!(memory, &[0x01, 0x01, 0x01, 0x02]);
    let global = &sections.iter().find(|s| s.0 == 6).unwrap().2;
    assert_eq!(global, &[0x01, 0x7F, 0x01, 0x41, 42, 0x0B]);
    let data = &sections.iter().find(|s| s.0 == 11).unwrap().2;
    assert_eq!(&data[..7], &[0x01, 0x00, 0x41, 0x00, 0x0B, 0x80, 0x80]);
    assert_eq!(&data[7..11], &[0x04, 1, 2, 3]);
}
