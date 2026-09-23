//! Generation of Wasm coredumps upon Wasm traps.
//!
//! The generated coredumps follow the [Wasm coredump format].
//!
//! [Wasm coredump format]: https://github.com/WebAssembly/tool-conventions/blob/main/Coredump.md

use crate::{
    Config, Error, Global, GlobalType, Handle as _, Instance, Memory, Module, Mutability, ValType,
    core::{CoreMemoryType as MemoryType, RawVal, ReadAs},
    engine::{CodeMap, EngineFunc, FrameSnapshot},
    instance::InstanceEntity,
    store::StoreInner,
};
use alloc::{boxed::Box, vec::Vec};
use core::{fmt, ops::Range, ptr};

/// The name of the single thread recorded in coredumps.
const THREAD_NAME: &str = "main";

/// A Wasm coredump captured upon a Wasm trap.
pub struct CoreDump {
    /// The name of the executable recorded in the coredump.
    executable_name: Box<str>,
    /// The modules of all instances referenced by the recorded frames.
    modules: Vec<CoreDumpModule>,
    /// The instances referenced by the recorded frames.
    instances: Vec<CoreDumpInstance>,
    /// The snapshots of all linear memories of the recorded instances.
    memories: Vec<CoreDumpMemory>,
    /// The snapshots of all global variables of the recorded instances.
    globals: Vec<CoreDumpGlobal>,
    /// The Wasm function frames ordered from youngest to oldest.
    frames: Vec<CoreDumpFrame>,
    /// The encoded coredump.
    bytes: Box<[u8]>,
}

impl fmt::Debug for CoreDump {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.debug_struct("CoreDump")
            .field("executable_name", &self.executable_name)
            .field("len_frames", &self.frames.len())
            .field("len_bytes", &self.bytes.len())
            .finish()
    }
}

/// A module recorded in a [`CoreDump`].
struct CoreDumpModule {
    module: Module,
    name: Box<str>,
}

/// An instance recorded in a [`CoreDump`].
struct CoreDumpInstance {
    instance: Instance,
    module: u32,
    memories: Vec<u32>,
    globals: Vec<u32>,
}

/// A snapshot of a linear memory recorded in a [`CoreDump`].
struct CoreDumpMemory {
    memory: Memory,
    ty: MemoryType,
    data: Box<[u8]>,
}

/// A snapshot of a global variable recorded in a [`CoreDump`].
struct CoreDumpGlobal {
    global: Global,
    ty: GlobalType,
    value: RawVal,
}

/// A Wasm function frame recorded in a [`CoreDump`].
struct CoreDumpFrame {
    instance: u32,
    func: u32,
    locals: Vec<CoreDumpValue>,
}

/// A value of a local variable recorded in a [`CoreDump`].
#[derive(Debug, Copy, Clone)]
enum CoreDumpValue {
    Missing,
    I32(i32),
    I64(i64),
    F32(u32),
    F64(u64),
}

impl CoreDump {
    /// Attaches a coredump to `error` or extends its existing coredump.
    ///
    /// - An existing coredump of `error` is extended with the Wasm `frames`
    ///   as those are older than the frames already recorded.
    /// - A new coredump is only generated if `error` is caused by a Wasm trap.
    /// - Does nothing if coredump generation is disabled in `config`.
    pub(crate) fn attach(
        config: &Config,
        store: &StoreInner,
        code: &CodeMap,
        frames: &[FrameSnapshot],
        error: &mut Error,
        is_wasm_trap: bool,
    ) {
        if !config.get_generate_coredump() || frames.is_empty() {
            return;
        }
        let mut coredump = match error.take_coredump() {
            Some(coredump) => coredump,
            None if is_wasm_trap && error.as_trap_code().is_some() => {
                Box::new(Self::new(config.get_coredump_executable_name()))
            }
            None => return,
        };
        coredump.push_frames(store, code, frames);
        coredump.bytes = coredump.encode().into();
        error.set_coredump(coredump);
    }

    /// Creates a new empty [`CoreDump`].
    fn new(executable_name: &str) -> Self {
        Self {
            executable_name: executable_name.into(),
            modules: Vec::new(),
            instances: Vec::new(),
            memories: Vec::new(),
            globals: Vec::new(),
            frames: Vec::new(),
            bytes: Box::from([]),
        }
    }

    /// Returns the encoded coredump bytes.
    pub(crate) fn as_bytes(&self) -> &[u8] {
        &self.bytes
    }

    /// Records the Wasm function `frames` ordered from youngest to oldest.
    fn push_frames(&mut self, store: &StoreInner, code: &CodeMap, frames: &[FrameSnapshot]) {
        let ranges = code.compiled_func_ranges();
        for frame in frames {
            let Some(func) = find_func(&ranges, frame.ip) else {
                continue;
            };
            let Some((instance, entity, module)) = resolve_instance(store, frame.instance, func)
            else {
                continue;
            };
            let Some((func_idx, local_types)) = module.func_locals(func) else {
                continue;
            };
            let instance = self.intern_instance(store, instance, entity, module);
            let locals = decode_locals(&local_types, frame.cells);
            self.frames.push(CoreDumpFrame {
                instance,
                func: func_idx.into_u32(),
                locals,
            });
        }
    }

    /// Returns the index of `instance` in `self` and records it if necessary.
    fn intern_instance(
        &mut self,
        store: &StoreInner,
        instance: Instance,
        entity: &InstanceEntity,
        module: &Module,
    ) -> u32 {
        if let Some(index) = self
            .instances
            .iter()
            .position(|recorded| recorded.instance.as_raw() == instance.as_raw())
        {
            return index as u32;
        }
        let module = self.intern_module(module);
        let memories = entity
            .memories()
            .iter()
            .map(|memory| self.intern_memory(store, memory))
            .collect();
        let globals = entity
            .globals()
            .iter()
            .map(|global| self.intern_global(store, global))
            .collect();
        self.instances.push(CoreDumpInstance {
            instance,
            module,
            memories,
            globals,
        });
        (self.instances.len() - 1) as u32
    }

    /// Returns the index of `module` in `self` and records it if necessary.
    fn intern_module(&mut self, module: &Module) -> u32 {
        if let Some(index) = self
            .modules
            .iter()
            .position(|recorded| recorded.module.ptr_eq(module))
        {
            return index as u32;
        }
        self.modules.push(CoreDumpModule {
            module: module.clone(),
            name: module.name().unwrap_or_default().into(),
        });
        (self.modules.len() - 1) as u32
    }

    /// Returns the index of `memory` in `self` and records a snapshot of it if necessary.
    fn intern_memory(&mut self, store: &StoreInner, memory: &Memory) -> u32 {
        if let Some(index) = self
            .memories
            .iter()
            .position(|recorded| recorded.memory.as_raw() == memory.as_raw())
        {
            return index as u32;
        }
        let entity = store.resolve_memory(memory);
        self.memories.push(CoreDumpMemory {
            memory: *memory,
            ty: entity.dynamic_ty(),
            data: entity.data().into(),
        });
        (self.memories.len() - 1) as u32
    }

    /// Returns the index of `global` in `self` and records a snapshot of it if necessary.
    fn intern_global(&mut self, store: &StoreInner, global: &Global) -> u32 {
        if let Some(index) = self
            .globals
            .iter()
            .position(|recorded| recorded.global.as_raw() == global.as_raw())
        {
            return index as u32;
        }
        let entity = store.resolve_global(global);
        self.globals.push(CoreDumpGlobal {
            global: *global,
            ty: entity.ty(),
            value: *entity.get_raw(),
        });
        (self.globals.len() - 1) as u32
    }

    /// Encodes `self` as Wasm binary in the Wasm coredump format.
    fn encode(&self) -> Vec<u8> {
        let mut out = Vec::new();
        out.extend_from_slice(b"\0asm");
        out.extend_from_slice(&[0x01, 0x00, 0x00, 0x00]);
        encode_custom_section(&mut out, "core", |out| {
            out.push(0x00);
            encode_name(out, &self.executable_name);
        });
        encode_custom_section(&mut out, "coremodules", |out| {
            encode_len(out, self.modules.len());
            for module in &self.modules {
                out.push(0x00);
                encode_name(out, &module.name);
            }
        });
        encode_custom_section(&mut out, "coreinstances", |out| {
            encode_len(out, self.instances.len());
            for instance in &self.instances {
                out.push(0x00);
                encode_u32(out, instance.module);
                encode_len(out, instance.memories.len());
                for &memory in &instance.memories {
                    encode_u32(out, memory);
                }
                encode_len(out, instance.globals.len());
                for &global in &instance.globals {
                    encode_u32(out, global);
                }
            }
        });
        encode_section(&mut out, SECTION_MEMORY, |out| {
            encode_len(out, self.memories.len());
            for memory in &self.memories {
                encode_memory_type(out, &memory.ty);
            }
        });
        encode_section(&mut out, SECTION_GLOBAL, |out| {
            encode_len(out, self.globals.len());
            for global in &self.globals {
                encode_global(out, global);
            }
        });
        encode_section(&mut out, SECTION_DATA, |out| {
            encode_len(out, self.memories.len());
            for (index, memory) in self.memories.iter().enumerate() {
                match index {
                    0 => out.push(0x00),
                    _ => {
                        out.push(0x02);
                        encode_len(out, index);
                    }
                }
                match memory.ty.is_64() {
                    true => out.extend_from_slice(&[OP_I64_CONST, 0x00, OP_END]),
                    false => out.extend_from_slice(&[OP_I32_CONST, 0x00, OP_END]),
                }
                encode_len(out, memory.data.len());
                out.extend_from_slice(&memory.data);
            }
        });
        encode_custom_section(&mut out, "corestack", |out| {
            out.push(0x00);
            encode_name(out, THREAD_NAME);
            encode_len(out, self.frames.len());
            for frame in &self.frames {
                out.push(0x00);
                encode_u32(out, frame.instance);
                encode_u32(out, frame.func);
                // Wasmi does not track Wasm code offsets of its frames.
                encode_u32(out, 0);
                encode_len(out, frame.locals.len());
                for &local in &frame.locals {
                    encode_value(out, local);
                }
                // Wasmi does not track the Wasm operand stack of its frames.
                encode_len(out, 0);
            }
        });
        out
    }
}

/// Returns the [`EngineFunc`] whose encoded ops contain the address `ip`.
///
/// The `ranges` must be sorted by their start address.
fn find_func(ranges: &[(Range<usize>, EngineFunc)], ip: usize) -> Option<EngineFunc> {
    let index = ranges.partition_point(|(range, _)| range.start <= ip);
    let (range, func) = ranges.get(index.checked_sub(1)?)?;
    range.contains(&ip).then_some(*func)
}

/// Resolves the [`Instance`] executing the internal function `func`.
///
/// Prefers the instance identified by `hint` and falls back to the first
/// instance of `store` whose module defines `func`.
fn resolve_instance<'a>(
    store: &'a StoreInner,
    hint: Option<*const InstanceEntity>,
    func: EngineFunc,
) -> Option<(Instance, &'a InstanceEntity, &'a Module)> {
    let defines_func = |entity: &'a InstanceEntity| {
        let module = entity.module()?;
        module.func_locals(func).is_some().then_some(module)
    };
    if let Some(hint) = hint {
        let found = store
            .instances()
            .find(|(_, entity)| ptr::eq(*entity, hint))
            .and_then(|(instance, entity)| Some((instance, entity, defines_func(entity)?)));
        if found.is_some() {
            return found;
        }
    }
    store
        .instances()
        .find_map(|(instance, entity)| Some((instance, entity, defines_func(entity)?)))
}

/// Decodes the values of the locals of types `local_types` from the frame's `cells`.
fn decode_locals(local_types: &[ValType], cells: &[crate::engine::Cell]) -> Vec<CoreDumpValue> {
    let mut offset = 0_usize;
    local_types
        .iter()
        .map(|ty| {
            let cell = cells.get(offset).copied().map(u64::from);
            offset += match ty {
                ValType::V128 => 2,
                _ => 1,
            };
            match (ty, cell) {
                (ValType::I32, Some(bits)) => CoreDumpValue::I32(bits as u32 as i32),
                (ValType::I64, Some(bits)) => CoreDumpValue::I64(bits as i64),
                (ValType::F32, Some(bits)) => CoreDumpValue::F32(bits as u32),
                (ValType::F64, Some(bits)) => CoreDumpValue::F64(bits),
                _ => CoreDumpValue::Missing,
            }
        })
        .collect()
}

const SECTION_CUSTOM: u8 = 0;
const SECTION_MEMORY: u8 = 5;
const SECTION_GLOBAL: u8 = 6;
const SECTION_DATA: u8 = 11;

const OP_END: u8 = 0x0B;
const OP_I32_CONST: u8 = 0x41;
const OP_I64_CONST: u8 = 0x42;
const OP_F32_CONST: u8 = 0x43;
const OP_F64_CONST: u8 = 0x44;
const OP_REF_NULL: u8 = 0xD0;
const OP_SIMD_PREFIX: u8 = 0xFD;
const OP_V128_CONST: u8 = 0x0C;

const TY_I32: u8 = 0x7F;
const TY_I64: u8 = 0x7E;
const TY_F32: u8 = 0x7D;
const TY_F64: u8 = 0x7C;
const TY_V128: u8 = 0x7B;
const TY_FUNCREF: u8 = 0x70;
const TY_EXTERNREF: u8 = 0x6F;

const VALUE_MISSING: u8 = 0x01;

/// Encodes a Wasm section with `id` and the contents written by `f`.
fn encode_section(out: &mut Vec<u8>, id: u8, f: impl FnOnce(&mut Vec<u8>)) {
    let mut contents = Vec::new();
    f(&mut contents);
    out.push(id);
    encode_len(out, contents.len());
    out.extend_from_slice(&contents);
}

/// Encodes a Wasm custom section named `name` with the payload written by `f`.
fn encode_custom_section(out: &mut Vec<u8>, name: &str, f: impl FnOnce(&mut Vec<u8>)) {
    encode_section(out, SECTION_CUSTOM, |out| {
        encode_name(out, name);
        f(out);
    });
}

/// Encodes `value` as unsigned LEB128.
fn encode_u64(out: &mut Vec<u8>, mut value: u64) {
    loop {
        let byte = (value & 0x7F) as u8;
        value >>= 7;
        if value == 0 {
            out.push(byte);
            return;
        }
        out.push(byte | 0x80);
    }
}

/// Encodes `value` as unsigned LEB128.
fn encode_u32(out: &mut Vec<u8>, value: u32) {
    encode_u64(out, u64::from(value))
}

/// Encodes the length or index `value` as unsigned LEB128.
fn encode_len(out: &mut Vec<u8>, value: usize) {
    encode_u64(out, value as u64)
}

/// Encodes `value` as signed LEB128.
fn encode_i64(out: &mut Vec<u8>, mut value: i64) {
    loop {
        let byte = (value & 0x7F) as u8;
        value >>= 7;
        let sign_bit_clear = byte & 0x40 == 0;
        if (value == 0 && sign_bit_clear) || (value == -1 && !sign_bit_clear) {
            out.push(byte);
            return;
        }
        out.push(byte | 0x80);
    }
}

/// Encodes `name` as LEB128 length-prefixed UTF-8.
fn encode_name(out: &mut Vec<u8>, name: &str) {
    encode_len(out, name.len());
    out.extend_from_slice(name.as_bytes());
}

/// Encodes a coredump value.
fn encode_value(out: &mut Vec<u8>, value: CoreDumpValue) {
    match value {
        CoreDumpValue::Missing => out.push(VALUE_MISSING),
        CoreDumpValue::I32(value) => {
            out.push(TY_I32);
            encode_i64(out, i64::from(value));
        }
        CoreDumpValue::I64(value) => {
            out.push(TY_I64);
            encode_i64(out, value);
        }
        CoreDumpValue::F32(bits) => {
            out.push(TY_F32);
            out.extend_from_slice(&bits.to_le_bytes());
        }
        CoreDumpValue::F64(bits) => {
            out.push(TY_F64);
            out.extend_from_slice(&bits.to_le_bytes());
        }
    }
}

/// Encodes the Wasm memory type `ty`.
fn encode_memory_type(out: &mut Vec<u8>, ty: &MemoryType) {
    let maximum = ty.maximum();
    let custom_page_size = ty.page_size_log2() != 16;
    let mut flags = 0x00;
    if maximum.is_some() {
        flags |= 0x01;
    }
    if ty.is_64() {
        flags |= 0x04;
    }
    if custom_page_size {
        flags |= 0x08;
    }
    out.push(flags);
    encode_u64(out, ty.minimum());
    if let Some(maximum) = maximum {
        encode_u64(out, maximum);
    }
    if custom_page_size {
        encode_u32(out, u32::from(ty.page_size_log2()));
    }
}

/// Encodes the Wasm global variable type and its current value as init expression.
fn encode_global(out: &mut Vec<u8>, global: &CoreDumpGlobal) {
    let content = global.ty.content();
    let ty = match content {
        ValType::I32 => TY_I32,
        ValType::I64 => TY_I64,
        ValType::F32 => TY_F32,
        ValType::F64 => TY_F64,
        ValType::V128 => TY_V128,
        ValType::FuncRef => TY_FUNCREF,
        ValType::ExternRef => TY_EXTERNREF,
    };
    out.push(ty);
    out.push(match global.ty.mutability() {
        Mutability::Const => 0x00,
        Mutability::Var => 0x01,
    });
    let value = &global.value;
    match content {
        ValType::I32 => {
            out.push(OP_I32_CONST);
            encode_i64(out, i64::from(ReadAs::<i32>::read_as(value)));
        }
        ValType::I64 => {
            out.push(OP_I64_CONST);
            encode_i64(out, ReadAs::<i64>::read_as(value));
        }
        ValType::F32 => {
            out.push(OP_F32_CONST);
            out.extend_from_slice(&ReadAs::<f32>::read_as(value).to_le_bytes());
        }
        ValType::F64 => {
            out.push(OP_F64_CONST);
            out.extend_from_slice(&ReadAs::<f64>::read_as(value).to_le_bytes());
        }
        ValType::V128 => {
            out.extend_from_slice(&[OP_SIMD_PREFIX, OP_V128_CONST]);
            out.extend_from_slice(&read_v128(value).to_le_bytes());
        }
        // Reference values cannot be represented in a coredump.
        ValType::FuncRef | ValType::ExternRef => {
            out.extend_from_slice(&[OP_REF_NULL, ty]);
        }
    }
    out.push(OP_END);
}

/// Reads the `v128` bits of `value`.
fn read_v128(value: &RawVal) -> u128 {
    #[cfg(feature = "simd")]
    {
        ReadAs::<crate::V128>::read_as(value).as_u128()
    }
    #[cfg(not(feature = "simd"))]
    {
        u128::from(ReadAs::<u64>::read_as(value))
    }
}
