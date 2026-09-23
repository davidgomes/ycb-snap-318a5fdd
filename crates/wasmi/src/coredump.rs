//! Wasm coredump generation upon Wasm traps.
//!
//! The format follows the Wasm tool-conventions coredump specification.

use crate::ValType;
use alloc::{boxed::Box, string::String, vec::Vec};
use wasmi_core::MemoryType;

/// A value recorded in a Wasm coredump.
#[derive(Debug, Copy, Clone)]
pub(crate) enum CoreValue {
    I32(i32),
    I64(i64),
    F32(u32),
    F64(u64),
    #[cfg_attr(not(feature = "simd"), allow(dead_code))]
    V128(u128),
    /// A value that could not be recovered, e.g. a reference value.
    Missing(ValType),
}

impl CoreValue {
    /// Creates a [`CoreValue`] of type `ty` from its raw 64-bit representation.
    pub fn from_bits(ty: ValType, bits: u64) -> Self {
        match ty {
            ValType::I32 => Self::I32(bits as u32 as i32),
            ValType::I64 => Self::I64(bits as i64),
            ValType::F32 => Self::F32(bits as u32),
            ValType::F64 => Self::F64(bits),
            ty => Self::Missing(ty),
        }
    }
}

/// A Wasm function frame recorded in a Wasm coredump.
#[derive(Debug)]
pub(crate) struct CoreFrame {
    pub instance: u32,
    pub func: u32,
    pub locals: Vec<CoreValue>,
}

#[derive(Debug)]
struct CoreInstance {
    key: usize,
    memories: Vec<u32>,
    globals: Vec<u32>,
}

#[derive(Debug)]
struct CoreMemory {
    key: usize,
    ty: MemoryType,
    data: Box<[u8]>,
}

#[derive(Debug)]
struct CoreGlobal {
    key: usize,
    mutable: bool,
    value: CoreValue,
}

/// A Wasm coredump attached to an [`Error`](crate::Error) caused by a Wasm trap.
#[derive(Debug)]
pub(crate) struct CoreDump {
    executable_name: String,
    instances: Vec<CoreInstance>,
    memories: Vec<CoreMemory>,
    globals: Vec<CoreGlobal>,
    frames: Vec<CoreFrame>,
    bytes: Box<[u8]>,
}

impl CoreDump {
    /// Creates a new empty [`CoreDump`].
    pub fn new(executable_name: &str) -> Self {
        Self {
            executable_name: executable_name.into(),
            instances: Vec::new(),
            memories: Vec::new(),
            globals: Vec::new(),
            frames: Vec::new(),
            bytes: Box::default(),
        }
    }

    /// Returns the encoded coredump bytes.
    pub fn bytes(&self) -> &[u8] {
        &self.bytes
    }

    /// Returns the index of the instance identified by `key` if already registered.
    pub fn find_instance(&self, key: usize) -> Option<u32> {
        self.instances
            .iter()
            .position(|instance| instance.key == key)
            .map(|index| index as u32)
    }

    /// Registers a new instance identified by `key` and returns its index.
    pub fn push_instance(&mut self, key: usize) -> u32 {
        let index = self.instances.len() as u32;
        self.instances.push(CoreInstance {
            key,
            memories: Vec::new(),
            globals: Vec::new(),
        });
        index
    }

    /// Adds a linear memory identified by `key` to the instance at `instance`.
    ///
    /// The `data` closure is only called if the memory has not been recorded before.
    pub fn add_memory(
        &mut self,
        instance: u32,
        key: usize,
        data: impl FnOnce() -> (MemoryType, Box<[u8]>),
    ) {
        let index = match self.memories.iter().position(|memory| memory.key == key) {
            Some(index) => index as u32,
            None => {
                let (ty, data) = data();
                self.memories.push(CoreMemory { key, ty, data });
                (self.memories.len() - 1) as u32
            }
        };
        self.instances[instance as usize].memories.push(index);
    }

    /// Adds a global variable identified by `key` to the instance at `instance`.
    pub fn add_global(&mut self, instance: u32, key: usize, mutable: bool, value: CoreValue) {
        let index = match self.globals.iter().position(|global| global.key == key) {
            Some(index) => index as u32,
            None => {
                self.globals.push(CoreGlobal {
                    key,
                    mutable,
                    value,
                });
                (self.globals.len() - 1) as u32
            }
        };
        self.instances[instance as usize].globals.push(index);
    }

    /// Appends an older frame to the stack of frames.
    pub fn push_frame(&mut self, frame: CoreFrame) {
        self.frames.push(frame);
    }

    /// Re-encodes the coredump bytes from the recorded state.
    pub fn encode(&mut self) {
        let mut out = Vec::new();
        out.extend_from_slice(b"\0asm");
        out.extend_from_slice(&1_u32.to_le_bytes());

        let mut payload = Vec::new();
        payload.push(0x00);
        write_name(&mut payload, &self.executable_name);
        write_custom_section(&mut out, "core", &payload);

        let mut payload = Vec::new();
        write_u32(&mut payload, self.instances.len() as u32);
        for _ in &self.instances {
            payload.push(0x00);
            write_name(&mut payload, "");
        }
        write_custom_section(&mut out, "coremodules", &payload);

        let mut payload = Vec::new();
        write_u32(&mut payload, self.instances.len() as u32);
        for (index, instance) in self.instances.iter().enumerate() {
            payload.push(0x00);
            write_u32(&mut payload, index as u32);
            write_u32(&mut payload, instance.memories.len() as u32);
            for &memory in &instance.memories {
                write_u32(&mut payload, memory);
            }
            write_u32(&mut payload, instance.globals.len() as u32);
            for &global in &instance.globals {
                write_u32(&mut payload, global);
            }
        }
        write_custom_section(&mut out, "coreinstances", &payload);

        let mut payload = Vec::new();
        payload.push(0x00);
        write_name(&mut payload, "main");
        write_u32(&mut payload, self.frames.len() as u32);
        for frame in &self.frames {
            payload.push(0x00);
            write_u32(&mut payload, frame.instance);
            write_u32(&mut payload, frame.func);
            write_u32(&mut payload, 0);
            write_u32(&mut payload, frame.locals.len() as u32);
            for local in &frame.locals {
                write_value(&mut payload, local);
            }
            write_u32(&mut payload, 0);
        }
        write_custom_section(&mut out, "corestack", &payload);

        if !self.memories.is_empty() {
            let mut payload = Vec::new();
            write_u32(&mut payload, self.memories.len() as u32);
            for memory in &self.memories {
                write_memory_type(&mut payload, &memory.ty);
            }
            write_section(&mut out, 5, &payload);
        }

        if !self.globals.is_empty() {
            let mut payload = Vec::new();
            write_u32(&mut payload, self.globals.len() as u32);
            for global in &self.globals {
                write_global(&mut payload, global);
            }
            write_section(&mut out, 6, &payload);
        }

        if !self.memories.is_empty() {
            let mut payload = Vec::new();
            write_u32(&mut payload, self.memories.len() as u32);
            for (index, memory) in self.memories.iter().enumerate() {
                if index == 0 {
                    payload.push(0x00);
                } else {
                    payload.push(0x02);
                    write_u32(&mut payload, index as u32);
                }
                if memory.ty.is_64() {
                    payload.push(0x42);
                } else {
                    payload.push(0x41);
                }
                payload.push(0x00);
                payload.push(0x0B);
                write_u32(&mut payload, memory.data.len() as u32);
                payload.extend_from_slice(&memory.data);
            }
            write_section(&mut out, 11, &payload);
        }

        self.bytes = out.into_boxed_slice();
    }
}

fn write_u64(out: &mut Vec<u8>, mut value: u64) {
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

fn write_u32(out: &mut Vec<u8>, value: u32) {
    write_u64(out, u64::from(value))
}

fn write_i64(out: &mut Vec<u8>, mut value: i64) {
    loop {
        let byte = (value & 0x7F) as u8;
        value >>= 7;
        let done = (value == 0 && byte & 0x40 == 0) || (value == -1 && byte & 0x40 != 0);
        if done {
            out.push(byte);
            return;
        }
        out.push(byte | 0x80);
    }
}

fn write_name(out: &mut Vec<u8>, name: &str) {
    write_u32(out, name.len() as u32);
    out.extend_from_slice(name.as_bytes());
}

fn write_section(out: &mut Vec<u8>, id: u8, payload: &[u8]) {
    out.push(id);
    write_u32(out, payload.len() as u32);
    out.extend_from_slice(payload);
}

fn write_custom_section(out: &mut Vec<u8>, name: &str, payload: &[u8]) {
    let mut section = Vec::with_capacity(payload.len() + name.len() + 5);
    write_name(&mut section, name);
    section.extend_from_slice(payload);
    write_section(out, 0, &section);
}

fn write_value(out: &mut Vec<u8>, value: &CoreValue) {
    match *value {
        CoreValue::I32(value) => {
            out.push(0x7F);
            write_i64(out, i64::from(value));
        }
        CoreValue::I64(value) => {
            out.push(0x7E);
            write_i64(out, value);
        }
        CoreValue::F32(bits) => {
            out.push(0x7D);
            out.extend_from_slice(&bits.to_le_bytes());
        }
        CoreValue::F64(bits) => {
            out.push(0x7C);
            out.extend_from_slice(&bits.to_le_bytes());
        }
        CoreValue::V128(_) | CoreValue::Missing(_) => out.push(0x01),
    }
}

fn val_type_byte(ty: ValType) -> u8 {
    match ty {
        ValType::I32 => 0x7F,
        ValType::I64 => 0x7E,
        ValType::F32 => 0x7D,
        ValType::F64 => 0x7C,
        ValType::V128 => 0x7B,
        ValType::FuncRef => 0x70,
        ValType::ExternRef => 0x6F,
    }
}

fn write_memory_type(out: &mut Vec<u8>, ty: &MemoryType) {
    let mut flags = 0x00;
    if ty.maximum().is_some() {
        flags |= 0x01;
    }
    if ty.is_64() {
        flags |= 0x04;
    }
    let custom_page_size = ty.page_size_log2() != 16;
    if custom_page_size {
        flags |= 0x08;
    }
    out.push(flags);
    write_u64(out, ty.minimum());
    if let Some(maximum) = ty.maximum() {
        write_u64(out, maximum);
    }
    if custom_page_size {
        write_u32(out, u32::from(ty.page_size_log2()));
    }
}

fn write_global(out: &mut Vec<u8>, global: &CoreGlobal) {
    let ty = match global.value {
        CoreValue::I32(_) => ValType::I32,
        CoreValue::I64(_) => ValType::I64,
        CoreValue::F32(_) => ValType::F32,
        CoreValue::F64(_) => ValType::F64,
        CoreValue::V128(_) => ValType::V128,
        CoreValue::Missing(ty) => ty,
    };
    out.push(val_type_byte(ty));
    out.push(u8::from(global.mutable));
    match global.value {
        CoreValue::I32(value) => {
            out.push(0x41);
            write_i64(out, i64::from(value));
        }
        CoreValue::I64(value) => {
            out.push(0x42);
            write_i64(out, value);
        }
        CoreValue::F32(bits) => {
            out.push(0x43);
            out.extend_from_slice(&bits.to_le_bytes());
        }
        CoreValue::F64(bits) => {
            out.push(0x44);
            out.extend_from_slice(&bits.to_le_bytes());
        }
        CoreValue::V128(bits) => {
            out.extend_from_slice(&[0xFD, 0x0C]);
            out.extend_from_slice(&bits.to_le_bytes());
        }
        CoreValue::Missing(ValType::V128) => {
            out.extend_from_slice(&[0xFD, 0x0C]);
            out.extend_from_slice(&[0x00; 16]);
        }
        CoreValue::Missing(ty) => {
            out.push(0xD0);
            out.push(val_type_byte(ty));
        }
    }
    out.push(0x0B);
}
