use crate::{
    Global, Instance, Memory, Mutability, ValType,
    core::ReadAs,
    engine::{Cell, CodeMap, EngineFunc, Inst, Stack},
    instance::InstanceEntity,
    store::{PrunedStore, StoreInner},
};
use alloc::{boxed::Box, vec, vec::Vec};

/// A snapshot of one Wasm function frame.
#[derive(Debug)]
pub(crate) struct FrameSnapshot {
    pub(crate) instance: Inst,
    pub(crate) func: EngineFunc,
    pub(crate) local_types: Vec<ValType>,
    pub(crate) cells: Vec<Cell>,
}

/// Serializes the current store and Wasm stacks into a Wasm coredump.
pub(crate) fn serialize(store: &PrunedStore, code: &CodeMap, stack: &Stack) -> Box<[u8]> {
    let inner = store.inner();
    let mut frames = stack.coredump_frames(code);
    frames.extend(inner.coredump_frames().map(|frame| FrameSnapshot {
        instance: frame.instance,
        func: frame.func,
        local_types: frame.local_types.clone(),
        cells: frame.cells.clone(),
    }));
    serialize_inner(
        inner,
        &frames,
        inner.engine().config().get_coredump_executable_name(),
    )
}

fn serialize_inner(
    store: &StoreInner,
    frames: &[FrameSnapshot],
    executable_name: &str,
) -> Box<[u8]> {
    let instances: Vec<_> = store.coredump_instances().collect();
    let memories: Vec<_> = store.coredump_memories().collect();
    let globals: Vec<_> = store.coredump_globals().collect();

    let mut modules: Vec<crate::module::ModuleHeader> = Vec::new();
    for (_, instance) in &instances {
        let Some(module) = instance.module() else {
            continue;
        };
        if !modules.iter().any(|known| known.same(module)) {
            modules.push(module.clone());
        }
    }

    let mut output = Vec::new();
    output.extend_from_slice(&[0x00, 0x61, 0x73, 0x6D, 0x01, 0x00, 0x00, 0x00]);
    write_core(&mut output, executable_name);
    write_modules(&mut output, &modules);
    write_instances(&mut output, &instances, &modules, &memories, &globals);
    write_stack(&mut output, &instances, frames);
    write_memories(&mut output, store, &memories);
    write_globals(&mut output, store, &globals);
    write_data(&mut output, store, &memories);
    output.into_boxed_slice()
}

fn write_core(output: &mut Vec<u8>, executable_name: &str) {
    let mut payload = vec![0x00];
    write_name(&mut payload, executable_name);
    write_custom(output, "core", &payload);
}

fn write_modules(output: &mut Vec<u8>, modules: &[crate::module::ModuleHeader]) {
    let mut payload = Vec::new();
    write_u32(&mut payload, modules.len() as u32);
    for _ in modules {
        payload.push(0x00);
        // Wasmi does not currently retain an optional Wasm module name.
        write_name(&mut payload, "");
    }
    write_custom(output, "coremodules", &payload);
}

fn write_instances(
    output: &mut Vec<u8>,
    instances: &[(Instance, &InstanceEntity)],
    modules: &[crate::module::ModuleHeader],
    memories: &[Memory],
    globals: &[Global],
) {
    let mut payload = Vec::new();
    write_u32(&mut payload, instances.len() as u32);
    for (_, instance) in instances {
        payload.push(0x00);
        let module_index = instance
            .module()
            .and_then(|module| modules.iter().position(|known| known.same(module)))
            .unwrap_or(0);
        write_u32(&mut payload, module_index as u32);
        write_handles(&mut payload, instance.memories(), memories);
        write_handles(&mut payload, instance.globals(), globals);
    }
    write_custom(output, "coreinstances", &payload);
}

fn write_handles<T>(payload: &mut Vec<u8>, handles: &[T], all: &[T])
where
    T: Copy + crate::Handle,
    T::Owned<crate::RawHandle<T>>: PartialEq,
{
    write_u32(payload, handles.len() as u32);
    for handle in handles {
        let index = all
            .iter()
            .position(|candidate| candidate.as_raw() == handle.as_raw())
            .unwrap_or(0);
        write_u32(payload, index as u32);
    }
}

fn write_stack(
    output: &mut Vec<u8>,
    instances: &[(Instance, &InstanceEntity)],
    frames: &[FrameSnapshot],
) {
    let mut payload = vec![0x00];
    write_name(&mut payload, "");
    write_u32(&mut payload, frames.len() as u32);
    for frame in frames {
        payload.push(0x00);
        let instance_index = instances
            .iter()
            .position(|(_, entity)| Inst::from(*entity) == frame.instance)
            .unwrap_or(0);
        write_u32(&mut payload, instance_index as u32);
        let func_index = instances
            .iter()
            .find(|(_, entity)| Inst::from(*entity) == frame.instance)
            .and_then(|(_, entity)| entity.module())
            .and_then(|module| module.get_func_index(frame.func))
            .map(|index| index.into_u32())
            .unwrap_or(0);
        write_u32(&mut payload, func_index);
        // Wasmi currently does not retain a Wasm-to-interpreter instruction map.
        write_u32(&mut payload, 0);
        write_locals(&mut payload, frame);
        // The optimized interpreter stack has no runtime type map. Values that
        // cannot be recovered are therefore represented by an empty vector.
        write_u32(&mut payload, 0);
    }
    write_custom(output, "corestack", &payload);
}

fn write_locals(payload: &mut Vec<u8>, frame: &FrameSnapshot) {
    write_u32(payload, frame.local_types.len() as u32);
    let mut cell_index = 0;
    for ty in &frame.local_types {
        if let Some(cell) = frame.cells.get(cell_index) {
            write_value(payload, *ty, *cell);
        } else {
            payload.push(0x01);
        }
        cell_index += usize::from(crate::engine::required_cells_for_ty(*ty));
    }
}

fn write_memories(output: &mut Vec<u8>, store: &StoreInner, memories: &[Memory]) {
    let mut payload = Vec::new();
    write_u32(&mut payload, memories.len() as u32);
    for memory in memories {
        let ty = store.resolve_memory(memory).ty();
        let flags = (if ty.maximum().is_some() { 1 } else { 0 }) | (if ty.is_64() { 4 } else { 0 });
        payload.push(flags);
        if ty.is_64() {
            write_u64(&mut payload, store.resolve_memory(memory).size());
            if let Some(maximum) = ty.maximum() {
                write_u64(&mut payload, maximum);
            }
        } else {
            write_u32(&mut payload, store.resolve_memory(memory).size() as u32);
            if let Some(maximum) = ty.maximum() {
                write_u32(&mut payload, maximum as u32);
            }
        }
    }
    write_section(output, 5, &payload);
}

fn write_globals(output: &mut Vec<u8>, store: &StoreInner, globals: &[Global]) {
    let mut payload = Vec::new();
    write_u32(&mut payload, globals.len() as u32);
    for global in globals {
        let entity = store.resolve_global(global);
        let ty = entity.ty();
        write_valtype(&mut payload, ty.content());
        payload.push(if matches!(ty.mutability(), Mutability::Var) {
            1
        } else {
            0
        });
        write_const_expr(&mut payload, ty.content(), entity.get_raw());
    }
    write_section(output, 6, &payload);
}

fn write_data(output: &mut Vec<u8>, store: &StoreInner, memories: &[Memory]) {
    let mut payload = Vec::new();
    write_u32(&mut payload, memories.len() as u32);
    for (index, memory) in memories.iter().enumerate() {
        payload.push(if index == 0 { 0x00 } else { 0x02 });
        if index != 0 {
            write_u32(&mut payload, index as u32);
        }
        payload.push(0x41);
        payload.push(0x00);
        payload.push(0x0B);
        let bytes = store.resolve_memory(memory).data();
        write_u32(&mut payload, bytes.len() as u32);
        payload.extend_from_slice(bytes);
    }
    write_section(output, 11, &payload);
}

fn write_const_expr(payload: &mut Vec<u8>, ty: ValType, raw: &crate::core::RawVal) {
    match ty {
        ValType::I32 => {
            payload.push(0x41);
            write_i32(payload, raw.read_as());
        }
        ValType::I64 => {
            payload.push(0x42);
            write_i64(payload, raw.read_as());
        }
        ValType::F32 => {
            payload.push(0x43);
            let value: f32 = raw.read_as();
            payload.extend_from_slice(&value.to_le_bytes());
        }
        ValType::F64 => {
            payload.push(0x44);
            let value: f64 = raw.read_as();
            payload.extend_from_slice(&value.to_le_bytes());
        }
        _ => {
            payload.push(0xD0);
            payload.push(if matches!(ty, ValType::FuncRef) {
                0x70
            } else {
                0x6F
            });
        }
    }
    payload.push(0x0B);
}

fn write_value(payload: &mut Vec<u8>, ty: ValType, cell: Cell) {
    match ty {
        ValType::I32 => {
            payload.push(0x7F);
            write_i32(payload, i32::from(cell));
        }
        ValType::I64 => {
            payload.push(0x7E);
            write_i64(payload, i64::from(cell));
        }
        ValType::F32 => {
            payload.push(0x7D);
            payload.extend_from_slice(&f32::from(cell).to_le_bytes());
        }
        ValType::F64 => {
            payload.push(0x7C);
            payload.extend_from_slice(&f64::from(cell).to_le_bytes());
        }
        _ => payload.push(0x01),
    }
}

fn write_valtype(payload: &mut Vec<u8>, ty: ValType) {
    payload.push(match ty {
        ValType::I32 => 0x7F,
        ValType::I64 => 0x7E,
        ValType::F32 => 0x7D,
        ValType::F64 => 0x7C,
        ValType::V128 => 0x7B,
        ValType::FuncRef => 0x70,
        ValType::ExternRef => 0x6F,
    });
}

fn write_custom(output: &mut Vec<u8>, name: &str, payload: &[u8]) {
    output.push(0);
    let size = name.len() + leb_size(name.len() as u32) + payload.len();
    write_u32(output, size as u32);
    write_name(output, name);
    output.extend_from_slice(payload);
}

fn write_section(output: &mut Vec<u8>, id: u8, payload: &[u8]) {
    output.push(id);
    write_u32(output, payload.len() as u32);
    output.extend_from_slice(payload);
}

fn write_name(output: &mut Vec<u8>, name: &str) {
    write_u32(output, name.len() as u32);
    output.extend_from_slice(name.as_bytes());
}

fn write_u32(output: &mut Vec<u8>, mut value: u32) {
    loop {
        let mut byte = (value & 0x7F) as u8;
        value >>= 7;
        if value != 0 {
            byte |= 0x80;
        }
        output.push(byte);
        if value == 0 {
            return;
        }
    }
}

fn write_u64(output: &mut Vec<u8>, mut value: u64) {
    loop {
        let mut byte = (value & 0x7F) as u8;
        value >>= 7;
        if value != 0 {
            byte |= 0x80;
        }
        output.push(byte);
        if value == 0 {
            return;
        }
    }
}

fn write_i32(output: &mut Vec<u8>, value: i32) {
    write_signed(output, value as i64, 32);
}

fn write_i64(output: &mut Vec<u8>, value: i64) {
    write_signed(output, value, 64);
}

fn write_signed(output: &mut Vec<u8>, mut value: i64, bits: u32) {
    let mut more = true;
    while more {
        let byte = (value as u8) & 0x7F;
        value >>= 7;
        let sign = byte & 0x40 != 0;
        more = !((value == 0 && !sign) || (value == -1 && sign));
        output.push(if more { byte | 0x80 } else { byte });
    }
    let _ = bits;
}

fn leb_size(mut value: u32) -> usize {
    let mut size = 1;
    while value >= 0x80 {
        value >>= 7;
        size += 1;
    }
    size
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::{Config, Engine, Instance, Module, Store};
    use wasmparser::{Parser, Payload};

    #[test]
    fn generated_coredump_is_valid_wasm() {
        let mut config = Config::default();
        config
            .generate_coredump(true)
            .coredump_executable_name("trapper");
        let engine = Engine::new(&config);
        let module = Module::new(
            &engine,
            r#"(module
                (memory 1)
                (global (mut i32) (i32.const 7))
                (func (export "trap") (param i32) (local i64)
                    (local.set 0 (i64.const 42))
                    (unreachable))
            )"#,
        )
        .unwrap();
        let mut store = Store::new(&engine, ());
        let instance = Instance::new(&mut store, &module, &[]).unwrap();
        let trap = instance
            .get_func(&store, "trap")
            .unwrap()
            .call(&mut store, &[crate::Val::I32(1)], &mut [])
            .unwrap_err();
        let dump = trap.coredump().expect("Wasm trap should have a coredump");
        let mut custom_sections = Vec::new();
        for payload in Parser::new(0).parse_all(dump) {
            match payload.unwrap() {
                Payload::CustomSection(section) => custom_sections.push(section.name()),
                _ => {}
            }
        }
        assert_eq!(
            custom_sections,
            ["core", "coremodules", "coreinstances", "corestack"]
        );
    }

    #[test]
    fn coredump_is_disabled_by_default() {
        let engine = Engine::default();
        let module = Module::new(&engine, "(module (func (export \"trap\") unreachable))").unwrap();
        let mut store = Store::new(&engine, ());
        let instance = Instance::new(&mut store, &module, &[]).unwrap();
        let trap = instance
            .get_func(&store, "trap")
            .unwrap()
            .call(&mut store, &[], &mut [])
            .unwrap_err();
        assert!(trap.coredump().is_none());
    }
}
