//! Debug metadata recorded while translating Wasm so a coredump can recover
//! locals, the operand stack, and Wasm code offsets.

use crate::ValType;
use alloc::boxed::Box;

/// Per-function metadata used only when coredump generation is enabled.
#[derive(Debug, Clone)]
pub struct FuncDebugInfo {
    /// Wasm function index within the module, including imports.
    pub func_index: u32,
    /// Declared type of every parameter and local, in index order.
    pub local_types: Box<[ValType]>,
    /// Value-stack cell offset of every local, relative to the frame base.
    pub local_offsets: Box<[u16]>,
    /// Operand-stack snapshots keyed by Wasmi bytecode offset.
    pub sites: Box<[DebugSite]>,
}

/// Operand stack and Wasm offset at one translated instruction.
#[derive(Debug, Clone)]
pub struct DebugSite {
    /// Byte offset of the instruction in the encoded Wasmi function.
    pub ir_offset: u32,
    /// Byte offset of the instruction within the Wasm function body.
    pub wasm_offset: u32,
    /// Logical Wasm operand stack before the instruction executes.
    pub operands: Box<[DebugOperand]>,
}

/// A recoverable operand value or the slot that holds it.
#[derive(Debug, Copy, Clone)]
pub enum DebugOperand {
    /// Value lives in a frame-relative value-stack slot.
    Slot { offset: u16, ty: ValType },
    /// Immediate `i32` that has not been materialized in a slot.
    ImmI32(i32),
    /// Immediate `i64` that has not been materialized in a slot.
    ImmI64(i64),
    /// Immediate `f32` bits that have not been materialized in a slot.
    ImmF32(u32),
    /// Immediate `f64` bits that have not been materialized in a slot.
    ImmF64(u64),
    /// The value cannot be represented in a coredump.
    Missing,
}

impl FuncDebugInfo {
    /// Returns the debug site that describes `ir_offset`.
    ///
    /// When `at_instr_start` is set, `ir_offset` points at the first byte of an
    /// instruction. Otherwise it points at the return address after that
    /// instruction, and the previous site is selected.
    pub fn site_for(&self, ir_offset: u32, at_instr_start: bool) -> Option<&DebugSite> {
        let idx = if at_instr_start {
            self.sites.partition_point(|site| site.ir_offset <= ir_offset)
        } else {
            self.sites.partition_point(|site| site.ir_offset < ir_offset)
        };
        idx.checked_sub(1).map(|idx| &self.sites[idx])
    }
}
