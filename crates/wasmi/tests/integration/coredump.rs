//! Tests for opt-in Wasm coredump generation.

use wasmi::{Caller, Config, Engine, Error, Extern, Linker, Module, Store};

fn engine(name: &str) -> Engine {
    let mut config = Config::default();
    config.generate_coredump(true);
    config.coredump_executable_name(name);
    Engine::new(&config)
}

#[test]
fn disabled_traps_have_no_coredump() {
    let engine = Engine::default();
    let mut store = Store::new(&engine, ());
    let module = Module::new(
        &engine,
        r#"
        (module
            (func (export "run")
                unreachable
            )
        )
        "#,
    )
    .unwrap();
    let instance = Linker::new(&engine)
        .instantiate_and_start(&mut store, &module)
        .unwrap();
    let func = instance.get_typed_func::<(), ()>(&store, "run").unwrap();
    let error = func.call(&mut store, ()).unwrap_err();
    assert!(error.as_trap_code().is_some());
    assert!(error.coredump().is_none());
}

#[test]
fn trap_coredump_contains_locals_memory_and_global() {
    let engine = engine("demo");
    let mut store = Store::new(&engine, ());
    let module = Module::new(
        &engine,
        r#"
        (module
            (memory (export "mem") 1)
            (global (export "g") (mut i32) (i32.const 7))
            (func (export "run") (param i32) (local i64)
                (i32.store (i32.const 0) (i32.const 2))
                (global.set 0 (i32.const 9))
                unreachable
            )
        )
        "#,
    )
    .unwrap();
    let instance = Linker::new(&engine)
        .instantiate_and_start(&mut store, &module)
        .unwrap();
    let func = instance.get_typed_func::<i32, ()>(&store, "run").unwrap();
    let error = func.call(&mut store, 42).unwrap_err();
    let mem = instance.get_memory(&store, "mem").unwrap();
    let mut live = [0u8; 4];
    mem.read(&store, 0, &mut live).unwrap();
    let global = instance.get_global(&store, "g").unwrap().get(&store);
    assert_eq!(live, [2, 0, 0, 0], "live memory {global:?}");
    let bytes = error.coredump().expect("coredump");
    assert_eq!(&bytes[..4], b"\0asm");
    let dump = parse(bytes);
    assert_eq!(dump.executable_name, "demo");
    assert_eq!(dump.modules, 1);
    assert_eq!(dump.instances, 1);
    assert_eq!(dump.memories, 1);
    assert_eq!(dump.globals, 1);
    assert_eq!(dump.frames.len(), 1);
    let frame = &dump.frames[0];
    assert_eq!(frame.func_index, 0);
    assert_eq!(frame.locals, vec![CoreNum::I32(42), CoreNum::I64(0)]);
    assert!(
        dump.memory_has_byte(0, 2),
        "mem len {} first {:?}",
        dump.memory.len(),
        dump.memory.get(..16.min(dump.memory.len()))
    );
    assert_eq!(dump.global_i32, Some(9));
}

#[test]
fn reentrant_host_call_extends_frames() {
    let engine = engine("");
    let mut store = Store::new(&engine, ());
    let mut linker = Linker::<()>::new(&engine);
    linker
        .func_wrap(
            "host",
            "call",
            |mut caller: Caller<()>| -> Result<(), Error> {
                let inner = caller
                    .get_export("inner")
                    .and_then(Extern::into_func)
                    .unwrap()
                    .typed::<(), ()>(&caller)
                    .unwrap();
                inner.call(&mut caller, ())
            },
        )
        .unwrap();
    let module = Module::new(
        &engine,
        r#"
        (module
            (import "host" "call" (func $host))
            (func (export "outer") (param i32)
                call $host
            )
            (func (export "inner")
                (local i32)
                (local.set 0 (i32.const 5))
                unreachable
            )
        )
        "#,
    )
    .unwrap();
    let instance = linker.instantiate_and_start(&mut store, &module).unwrap();
    let func = instance.get_typed_func::<i32, ()>(&store, "outer").unwrap();
    let error = func.call(&mut store, 5).unwrap_err();
    let dump = parse(error.coredump().expect("coredump"));
    assert_eq!(dump.frames.len(), 2);
    assert_eq!(dump.frames[0].func_index, 2);
    assert_eq!(dump.frames[0].locals, vec![CoreNum::I32(5)]);
    assert_eq!(dump.frames[1].func_index, 1);
    assert_eq!(dump.frames[1].locals, vec![CoreNum::I32(5)]);
    assert!(dump.frames.iter().all(|frame| frame.instance == 0));
}

#[derive(Debug, PartialEq)]
enum CoreNum {
    I32(i32),
    I64(i64),
}

struct Frame {
    func_index: u32,
    instance: u32,
    locals: Vec<CoreNum>,
}

struct Dump {
    executable_name: String,
    modules: usize,
    instances: usize,
    memories: usize,
    globals: usize,
    frames: Vec<Frame>,
    memory: Vec<u8>,
    global_i32: Option<i32>,
}

impl Dump {
    fn memory_has_byte(&self, index: usize, byte: u8) -> bool {
        self.memory.get(index).copied() == Some(byte)
    }
}

fn parse(bytes: &[u8]) -> Dump {
    assert_eq!(&bytes[..8], b"\0asm\x01\0\0\0");
    let mut pos = 8;
    let mut executable_name = String::new();
    let mut modules = 0;
    let mut instances = 0;
    let mut memories = 0;
    let mut globals = 0;
    let mut frames = Vec::new();
    let mut memory = Vec::new();
    let mut global_i32 = None;
    while pos < bytes.len() {
        let id = bytes[pos];
        pos += 1;
        let size = read_uleb(bytes, &mut pos) as usize;
        let end = pos + size;
        let body = &bytes[pos..end];
        pos = end;
        if id == 0 {
            let mut cursor = 0;
            let name = read_name(body, &mut cursor);
            let payload = &body[cursor..];
            match name.as_str() {
                "core" => {
                    assert_eq!(payload[0], 0x00);
                    let mut cursor = 1;
                    executable_name = read_name(payload, &mut cursor);
                }
                "coremodules" => {
                    let mut cursor = 0;
                    modules = read_uleb(payload, &mut cursor) as usize;
                }
                "coreinstances" => {
                    let mut cursor = 0;
                    instances = read_uleb(payload, &mut cursor) as usize;
                }
                "corestack" => {
                    let mut cursor = 0;
                    assert_eq!(payload[cursor], 0x00);
                    cursor += 1;
                    let _thread = read_name(payload, &mut cursor);
                    let count = read_uleb(payload, &mut cursor);
                    for _ in 0..count {
                        assert_eq!(payload[cursor], 0x00);
                        cursor += 1;
                        let instance = read_uleb(payload, &mut cursor) as u32;
                        let func_index = read_uleb(payload, &mut cursor) as u32;
                        let _offset = read_uleb(payload, &mut cursor);
                        let locals = read_values(payload, &mut cursor);
                        let _stack = read_values(payload, &mut cursor);
                        frames.push(Frame {
                            func_index,
                            instance,
                            locals,
                        });
                    }
                }
                _ => {}
            }
        } else if id == 6 {
            let mut cursor = 0;
            globals = read_uleb(body, &mut cursor) as usize;
            if globals > 0 {
                let _ty = body[cursor];
                cursor += 1;
                let _mut = body[cursor];
                cursor += 1;
                assert_eq!(body[cursor], 0x41);
                cursor += 1;
                global_i32 = Some(read_sleb(body, &mut cursor) as i32);
            }
        } else if id == 5 {
            let mut cursor = 0;
            memories = read_uleb(body, &mut cursor) as usize;
        } else if id == 11 {
            let mut cursor = 0;
            let count = read_uleb(body, &mut cursor);
            if count > 0 {
                let flags = body[cursor];
                cursor += 1;
                if flags == 0x02 {
                    let _mem = read_uleb(body, &mut cursor);
                }
                assert_eq!(body[cursor], 0x41);
                cursor += 1;
                let _offset = read_sleb(body, &mut cursor);
                assert_eq!(body[cursor], 0x0B);
                cursor += 1;
                let len = read_uleb(body, &mut cursor) as usize;
                memory = body[cursor..cursor + len].to_vec();
            }
        }
    }
    Dump {
        executable_name,
        modules,
        instances,
        memories,
        globals,
        frames,
        memory,
        global_i32,
    }
}

fn read_values(bytes: &[u8], pos: &mut usize) -> Vec<CoreNum> {
    let count = read_uleb(bytes, pos);
    let mut values = Vec::new();
    for _ in 0..count {
        let tag = bytes[*pos];
        *pos += 1;
        match tag {
            0x7F => values.push(CoreNum::I32(read_sleb(bytes, pos) as i32)),
            0x7E => values.push(CoreNum::I64(read_sleb(bytes, pos))),
            0x7D => *pos += 4,
            0x7C => *pos += 8,
            0x01 => {}
            other => panic!("unexpected value tag {other:#x}"),
        }
    }
    values
}

fn read_name(bytes: &[u8], pos: &mut usize) -> String {
    let len = read_uleb(bytes, pos) as usize;
    let name = std::str::from_utf8(&bytes[*pos..*pos + len])
        .unwrap()
        .to_string();
    *pos += len;
    name
}

fn read_uleb(bytes: &[u8], pos: &mut usize) -> u64 {
    let mut result = 0u64;
    let mut shift = 0;
    loop {
        let byte = bytes[*pos];
        *pos += 1;
        result |= u64::from(byte & 0x7F) << shift;
        if byte & 0x80 == 0 {
            return result;
        }
        shift += 7;
    }
}

fn read_sleb(bytes: &[u8], pos: &mut usize) -> i64 {
    let mut result = 0i64;
    let mut shift = 0;
    loop {
        let byte = bytes[*pos];
        *pos += 1;
        result |= i64::from(byte & 0x7F) << shift;
        shift += 7;
        if byte & 0x80 == 0 {
            if shift < 64 && byte & 0x40 != 0 {
                result |= !0 << shift;
            }
            return result;
        }
    }
}
