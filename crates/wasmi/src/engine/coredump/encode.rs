//! Encoding of [`Contents`] as Wasm coredump binary.

use super::{Contents, CoreDump, FrameEntry, GlobalEntry, MemoryData, Value};
use crate::{
    Mutability,
    ValType,
    core::{CoreMemoryType, RawVal, ReadAs},
};
use alloc::vec::Vec;

/// The name of the thread of all recorded frames.
const THREAD_NAME: &str = "main";

const SECTION_CUSTOM: u8 = 0x00;
const SECTION_MEMORY: u8 = 0x05;
const SECTION_GLOBAL: u8 = 0x06;
const SECTION_DATA: u8 = 0x0B;

const OP_END: u8 = 0x0B;
const OP_I32_CONST: u8 = 0x41;
const OP_I64_CONST: u8 = 0x42;
const OP_F32_CONST: u8 = 0x43;
const OP_F64_CONST: u8 = 0x44;
const OP_REF_NULL: u8 = 0xD0;
const OP_SIMD_PREFIX: u8 = 0xFD;
const OP_V128_CONST: u32 = 0x0C;

/// The default page size of Wasm linear memories in log2(bytes).
const DEFAULT_PAGE_SIZE_LOG2: u8 = 16;

impl Contents {
    /// Encodes `self` as Wasm coredump binary.
    ///
    /// The `bytes` are the bytes of the previously encoded [`CoreDump`] referred to by
    /// [`MemoryData::Encoded`] ranges of `self`, if any.
    ///
    /// Returns `None` if `self` is too large to be encoded as Wasm binary.
    pub(super) fn encode(mut self, bytes: &[u8]) -> Option<CoreDump> {
        let mut out = Vec::from(*b"\0asm\x01\0\0\0");
        let mut section = Vec::new();

        section.push(0x00);
        write_name(&mut section, &self.executable_name)?;
        write_custom_section(&mut out, "core", &section)?;

        section.clear();
        write_len(&mut section, self.memories.len())?;
        for memory in &self.memories {
            write_memory_type(&mut section, memory.ty);
        }
        write_section(&mut out, SECTION_MEMORY, &section)?;

        section.clear();
        write_len(&mut section, self.globals.len())?;
        for global in &self.globals {
            write_global(&mut section, global);
        }
        write_section(&mut out, SECTION_GLOBAL, &section)?;

        self.write_data_section(&mut out, bytes)?;

        section.clear();
        write_len(&mut section, self.modules.len())?;
        for module in &self.modules {
            section.push(0x00);
            write_name(&mut section, &module.name)?;
        }
        write_custom_section(&mut out, "coremodules", &section)?;

        section.clear();
        write_len(&mut section, self.instances.len())?;
        for instance in &self.instances {
            section.push(0x00);
            write_u32(&mut section, instance.module);
            write_u32_vec(&mut section, &instance.memories)?;
            write_u32_vec(&mut section, &instance.globals)?;
        }
        write_custom_section(&mut out, "coreinstances", &section)?;

        section.clear();
        section.push(0x00);
        write_name(&mut section, THREAD_NAME)?;
        write_len(&mut section, self.frames.len())?;
        for frame in &self.frames {
            write_frame(&mut section, frame)?;
        }
        write_custom_section(&mut out, "corestack", &section)?;

        Some(CoreDump {
            bytes: out.into_boxed_slice(),
            contents: self,
        })
    }

    /// Writes the data section with the contents of all recorded linear memories to `out`.
    ///
    /// Afterwards the contents of all recorded linear memories refer to their range in `out`.
    ///
    /// # Note
    ///
    /// The contents are written directly to `out` since they might be large.
    fn write_data_section(&mut self, out: &mut Vec<u8>, bytes: &[u8]) -> Option<()> {
        let mut len_segments = Vec::new();
        write_len(&mut len_segments, self.memories.len())?;
        let mut len_section = len_segments.len();
        let mut headers = Vec::with_capacity(self.memories.len());
        for (index, memory) in self.memories.iter().enumerate() {
            let len_data = memory.data(bytes).len();
            let header = data_segment_header(index as u32, memory.ty.is_64(), len_data)?;
            len_section = len_section
                .checked_add(header.len())?
                .checked_add(len_data)?;
            headers.push(header);
        }
        out.push(SECTION_DATA);
        write_len(out, len_section)?;
        out.extend_from_slice(&len_segments);
        for (memory, header) in self.memories.iter_mut().zip(headers) {
            out.extend_from_slice(&header);
            let start = out.len();
            out.extend_from_slice(memory.data(bytes));
            memory.data = MemoryData::Encoded(start..out.len());
        }
        Some(())
    }
}

/// Returns the encoded active data segment up to its data for a linear memory at `memory_index`.
fn data_segment_header(memory_index: u32, is_64: bool, len_data: usize) -> Option<Vec<u8>> {
    let mut header = Vec::new();
    match memory_index {
        0 => header.push(0x00),
        index => {
            header.push(0x02);
            write_u32(&mut header, index);
        }
    }
    let offset_op = match is_64 {
        true => OP_I64_CONST,
        false => OP_I32_CONST,
    };
    header.extend([offset_op, 0x00, OP_END]);
    write_len(&mut header, len_data)?;
    Some(header)
}

/// Writes the linear memory type `ty` to `buffer`.
fn write_memory_type(buffer: &mut Vec<u8>, ty: CoreMemoryType) {
    let maximum = ty.maximum();
    let page_size_log2 = ty.page_size_log2();
    let has_custom_page_size = page_size_log2 != DEFAULT_PAGE_SIZE_LOG2;
    let mut flags = 0x00;
    if maximum.is_some() {
        flags |= 0x01;
    }
    if ty.is_64() {
        flags |= 0x04;
    }
    if has_custom_page_size {
        flags |= 0x08;
    }
    buffer.push(flags);
    write_u64(buffer, ty.minimum());
    if let Some(maximum) = maximum {
        write_u64(buffer, maximum);
    }
    if has_custom_page_size {
        write_u32(buffer, u32::from(page_size_log2));
    }
}

/// Writes the global variable type and its value as initializer expression to `buffer`.
fn write_global(buffer: &mut Vec<u8>, global: &GlobalEntry) {
    let ty = global.ty.content();
    let value = global.value;
    buffer.push(val_type_byte(ty));
    buffer.push(match global.ty.mutability() {
        Mutability::Const => 0x00,
        Mutability::Var => 0x01,
    });
    match ty {
        ValType::I32 => {
            buffer.push(OP_I32_CONST);
            write_i64(buffer, i64::from(<RawVal as ReadAs<i32>>::read_as(&value)));
        }
        ValType::I64 => {
            buffer.push(OP_I64_CONST);
            write_i64(buffer, value.read_as());
        }
        ValType::F32 => {
            buffer.push(OP_F32_CONST);
            buffer.extend(<RawVal as ReadAs<u32>>::read_as(&value).to_le_bytes());
        }
        ValType::F64 => {
            buffer.push(OP_F64_CONST);
            buffer.extend(<RawVal as ReadAs<u64>>::read_as(&value).to_le_bytes());
        }
        ValType::V128 => {
            buffer.push(OP_SIMD_PREFIX);
            write_u32(buffer, OP_V128_CONST);
            buffer.extend(v128_bits(value).to_le_bytes());
        }
        // Note: references cannot be expressed by constant expressions of the coredump.
        ValType::FuncRef | ValType::ExternRef => {
            buffer.extend([OP_REF_NULL, val_type_byte(ty)]);
        }
    }
    buffer.push(OP_END);
}

/// Returns the bits of the `v128` stored in `value`.
fn v128_bits(value: RawVal) -> u128 {
    #[cfg(feature = "simd")]
    {
        crate::V128::from(value).as_u128()
    }
    #[cfg(not(feature = "simd"))]
    {
        // Note: `v128` values cannot exist without the `simd` crate feature.
        let _ = value;
        0
    }
}

/// Writes the Wasm function `frame` to `buffer`.
fn write_frame(buffer: &mut Vec<u8>, frame: &FrameEntry) -> Option<()> {
    buffer.push(0x00);
    write_u32(buffer, frame.instance);
    write_u32(buffer, frame.func);
    // Note: Wasmi bytecode does not preserve the code offsets of the original Wasm instructions.
    write_u32(buffer, 0);
    write_len(buffer, frame.locals.len())?;
    for &local in &frame.locals {
        write_value(buffer, local);
    }
    // Note: Wasmi bytecode is register based and thus has no Wasm operand stack to recover.
    write_u32(buffer, 0);
    Some(())
}

/// Writes the coredump `value` to `buffer`.
fn write_value(buffer: &mut Vec<u8>, value: Value) {
    match value {
        Value::Missing => buffer.push(0x01),
        Value::I32(value) => {
            buffer.push(val_type_byte(ValType::I32));
            write_i64(buffer, i64::from(value));
        }
        Value::I64(value) => {
            buffer.push(val_type_byte(ValType::I64));
            write_i64(buffer, value);
        }
        Value::F32(bits) => {
            buffer.push(val_type_byte(ValType::F32));
            buffer.extend(bits.to_le_bytes());
        }
        Value::F64(bits) => {
            buffer.push(val_type_byte(ValType::F64));
            buffer.extend(bits.to_le_bytes());
        }
    }
}

/// Returns the Wasm binary encoding of `ty`.
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

/// Writes a section with `id` and `contents` to `out`.
fn write_section(out: &mut Vec<u8>, id: u8, contents: &[u8]) -> Option<()> {
    out.push(id);
    write_len(out, contents.len())?;
    out.extend_from_slice(contents);
    Some(())
}

/// Writes a custom section with `name` and `contents` to `out`.
fn write_custom_section(out: &mut Vec<u8>, name: &str, contents: &[u8]) -> Option<()> {
    let mut encoded_name = Vec::new();
    write_name(&mut encoded_name, name)?;
    out.push(SECTION_CUSTOM);
    write_len(out, encoded_name.len().checked_add(contents.len())?)?;
    out.extend_from_slice(&encoded_name);
    out.extend_from_slice(contents);
    Some(())
}

/// Writes `name` as length-prefixed UTF-8 to `buffer`.
fn write_name(buffer: &mut Vec<u8>, name: &str) -> Option<()> {
    write_len(buffer, name.len())?;
    buffer.extend_from_slice(name.as_bytes());
    Some(())
}

/// Writes `items` as length-prefixed vector of `u32` to `buffer`.
fn write_u32_vec(buffer: &mut Vec<u8>, items: &[u32]) -> Option<()> {
    write_len(buffer, items.len())?;
    for &item in items {
        write_u32(buffer, item);
    }
    Some(())
}

/// Writes `len` as `u32` to `buffer`.
///
/// Returns `None` if `len` does not fit into a `u32`.
fn write_len(buffer: &mut Vec<u8>, len: usize) -> Option<()> {
    write_u32(buffer, u32::try_from(len).ok()?);
    Some(())
}

/// Writes `value` using unsigned LEB128 encoding to `buffer`.
fn write_u32(buffer: &mut Vec<u8>, value: u32) {
    write_u64(buffer, u64::from(value))
}

/// Writes `value` using unsigned LEB128 encoding to `buffer`.
fn write_u64(buffer: &mut Vec<u8>, mut value: u64) {
    loop {
        let byte = (value & 0x7F) as u8;
        value >>= 7;
        if value == 0 {
            buffer.push(byte);
            return;
        }
        buffer.push(byte | 0x80);
    }
}

/// Writes `value` using signed LEB128 encoding to `buffer`.
fn write_i64(buffer: &mut Vec<u8>, mut value: i64) {
    loop {
        let byte = (value & 0x7F) as u8;
        value >>= 7;
        let is_sign_bit_set = byte & 0x40 != 0;
        if (value == 0 && !is_sign_bit_set) || (value == -1 && is_sign_bit_set) {
            buffer.push(byte);
            return;
        }
        buffer.push(byte | 0x80);
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn encode_u64(value: u64) -> Vec<u8> {
        let mut buffer = Vec::new();
        write_u64(&mut buffer, value);
        buffer
    }

    fn encode_i64(value: i64) -> Vec<u8> {
        let mut buffer = Vec::new();
        write_i64(&mut buffer, value);
        buffer
    }

    #[test]
    fn unsigned_leb128_works() {
        assert_eq!(encode_u64(0), [0x00]);
        assert_eq!(encode_u64(1), [0x01]);
        assert_eq!(encode_u64(127), [0x7F]);
        assert_eq!(encode_u64(128), [0x80, 0x01]);
        assert_eq!(encode_u64(624_485), [0xE5, 0x8E, 0x26]);
        assert_eq!(
            encode_u64(u64::from(u32::MAX)),
            [0xFF, 0xFF, 0xFF, 0xFF, 0x0F]
        );
    }

    #[test]
    fn signed_leb128_works() {
        assert_eq!(encode_i64(0), [0x00]);
        assert_eq!(encode_i64(1), [0x01]);
        assert_eq!(encode_i64(-1), [0x7F]);
        assert_eq!(encode_i64(63), [0x3F]);
        assert_eq!(encode_i64(64), [0xC0, 0x00]);
        assert_eq!(encode_i64(-64), [0x40]);
        assert_eq!(encode_i64(-65), [0xBF, 0x7F]);
        assert_eq!(encode_i64(-123_456), [0xC0, 0xBB, 0x78]);
        assert_eq!(
            encode_i64(i64::from(i32::MIN)),
            [0x80, 0x80, 0x80, 0x80, 0x78]
        );
        assert_eq!(
            encode_i64(i64::MAX),
            [0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0x00]
        );
    }
}
