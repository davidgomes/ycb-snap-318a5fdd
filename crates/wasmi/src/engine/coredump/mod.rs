//! Generation of Wasm coredumps for Wasm traps.
//!
//! Generated coredumps follow the [Wasm coredump] conventions.
//!
//! [Wasm coredump]: https://github.com/WebAssembly/tool-conventions/blob/main/Coredump.md

mod encode;

use super::{
    Cell,
    CodeMap,
    EngineFunc,
    Inst,
    Stack,
    executor::FrameView,
    translator::required_cells_for_ty,
};
use crate::{
    Config,
    Global,
    GlobalType,
    Handle,
    Instance,
    Memory,
    Module,
    ValType,
    core::{CoreMemoryType, RawVal},
    store::StoreInner,
};
use alloc::{boxed::Box, collections::BTreeMap, vec::Vec};
use core::{fmt, ops::Range};
use wasmparser::{BinaryReader, Name, NameSectionReader};

/// A Wasm coredump capturing the state of a Wasm execution at the time it trapped.
pub struct CoreDump {
    /// The coredump encoded as Wasm binary.
    bytes: Box<[u8]>,
    /// The contents of `bytes` required to extend the coredump with more frames.
    contents: Contents,
}

impl fmt::Debug for CoreDump {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.debug_struct("CoreDump")
            .field("len_bytes", &self.bytes.len())
            .field("len_frames", &self.contents.frames.len())
            .finish_non_exhaustive()
    }
}

impl CoreDump {
    /// Creates a new [`CoreDump`] of the trapped Wasm execution operating on `stack`.
    ///
    /// Returns `None` if `stack` has no Wasm frames or if the coredump is too large to be encoded.
    pub fn new(config: &Config, store: &StoreInner, stack: &Stack, code_map: &CodeMap) -> Option<Self> {
        let mut contents = Contents::new(config.get_coredump_executable_name());
        contents.push_frames(store, stack, code_map);
        if contents.frames.is_empty() {
            return None;
        }
        contents.encode(&[])
    }

    /// Extends `self` with the Wasm frames of `stack` which are older than all frames of `self`.
    ///
    /// # Note
    ///
    /// This is required when a Wasm trap propagates through a host function since the
    /// Wasm execution that called the host function operates on a different [`Stack`].
    pub fn extend(&mut self, store: &StoreInner, stack: &Stack, code_map: &CodeMap) {
        if stack.frames().next().is_none() {
            return;
        }
        let mut contents = self.contents.clone();
        contents.push_frames(store, stack, code_map);
        if let Some(extended) = contents.encode(&self.bytes) {
            *self = extended;
        }
    }

    /// Returns the bytes of the encoded [`CoreDump`].
    pub fn as_bytes(&self) -> &[u8] {
        &self.bytes
    }
}

/// The contents of a [`CoreDump`].
#[derive(Clone)]
struct Contents {
    /// The name of the executable.
    executable_name: Box<str>,
    /// The Wasm modules of all recorded instances.
    modules: Vec<ModuleEntry>,
    /// The recorded Wasm instances.
    instances: Vec<InstanceEntry>,
    /// The linear memories of all recorded instances.
    memories: Vec<MemoryEntry>,
    /// The global variables of all recorded instances.
    globals: Vec<GlobalEntry>,
    /// The recorded Wasm frames ordered from youngest to oldest.
    frames: Vec<FrameEntry>,
}

/// A Wasm module recorded in a [`CoreDump`].
#[derive(Clone)]
struct ModuleEntry {
    /// The recorded Wasm module.
    module: Module,
    /// The name of the Wasm module.
    name: Box<str>,
}

/// A Wasm instance recorded in a [`CoreDump`].
#[derive(Clone)]
struct InstanceEntry {
    /// The recorded Wasm instance.
    instance: Instance,
    /// The index of the module of the instance within [`Contents::modules`].
    module: u32,
    /// The indices of the linear memories of the instance within [`Contents::memories`].
    memories: Box<[u32]>,
    /// The indices of the global variables of the instance within [`Contents::globals`].
    globals: Box<[u32]>,
}

/// A linear memory recorded in a [`CoreDump`].
#[derive(Clone)]
struct MemoryEntry {
    /// The recorded linear memory.
    memory: Memory,
    /// The type of the linear memory with its size at the time of recording as minimum.
    ty: CoreMemoryType,
    /// The contents of the linear memory at the time of recording.
    data: MemoryData,
}

/// The contents of a linear memory recorded in a [`CoreDump`].
#[derive(Clone)]
enum MemoryData {
    /// The contents that have not yet been encoded.
    Snapshot(Box<[u8]>),
    /// The range of the contents within the bytes of the encoded [`CoreDump`].
    Encoded(Range<usize>),
}

impl MemoryEntry {
    /// Returns the contents of the linear memory.
    ///
    /// The `bytes` are the bytes of the encoded [`CoreDump`] that contains `self`, if any.
    fn data<'a>(&'a self, bytes: &'a [u8]) -> &'a [u8] {
        match &self.data {
            MemoryData::Snapshot(data) => data,
            MemoryData::Encoded(range) => &bytes[range.clone()],
        }
    }
}

/// A global variable recorded in a [`CoreDump`].
#[derive(Clone)]
struct GlobalEntry {
    /// The recorded global variable.
    global: Global,
    /// The type of the global variable.
    ty: GlobalType,
    /// The value of the global variable at the time of recording.
    value: RawVal,
}

/// A Wasm function frame recorded in a [`CoreDump`].
#[derive(Clone)]
struct FrameEntry {
    /// The index of the instance of the frame within [`Contents::instances`].
    instance: u32,
    /// The index of the executed function within its Wasm module.
    func: u32,
    /// The values of the function parameters followed by the function locals.
    locals: Box<[Value]>,
}

/// A value recorded in a [`CoreDump`].
#[derive(Copy, Clone)]
enum Value {
    /// The value could not be recovered.
    Missing,
    I32(i32),
    I64(i64),
    /// The bits of an `f32` value.
    F32(u32),
    /// The bits of an `f64` value.
    F64(u64),
}

impl Value {
    /// Reads a value of type `ty` from `cell`.
    fn from_cell(cell: Cell, ty: ValType) -> Self {
        match ty {
            ValType::I32 => Self::I32(i32::from(cell)),
            ValType::I64 => Self::I64(i64::from(cell)),
            ValType::F32 => Self::F32(u32::from(cell)),
            ValType::F64 => Self::F64(u64::from(cell)),
            // Note: the coredump format cannot represent values of these types.
            ValType::V128 | ValType::FuncRef | ValType::ExternRef => Self::Missing,
        }
    }
}

impl Contents {
    /// Creates new empty [`Contents`] for the executable with the given name.
    fn new(executable_name: &str) -> Self {
        Self {
            executable_name: executable_name.into(),
            modules: Vec::new(),
            instances: Vec::new(),
            memories: Vec::new(),
            globals: Vec::new(),
            frames: Vec::new(),
        }
    }

    /// Pushes the Wasm frames of `stack` from youngest to oldest.
    ///
    /// Also records all instances of `store` that have not yet been recorded.
    fn push_frames(&mut self, store: &StoreInner, stack: &Stack, code_map: &CodeMap) {
        self.push_instances(store);
        let entities: Vec<Option<Inst>> = self
            .instances
            .iter()
            .map(|entry| store.try_resolve_instance(&entry.instance).ok().map(Inst::from))
            .collect();
        let mut funcs = FuncLookup::default();
        for frame in stack.frames() {
            if let Some(entry) = self.frame_entry(&entities, &mut funcs, code_map, frame) {
                self.frames.push(entry);
            }
        }
    }

    /// Returns the [`FrameEntry`] for `frame` if its function can be identified.
    ///
    /// The `entities` are the [`Inst`]s of the recorded instances in `store` if any.
    fn frame_entry(
        &self,
        entities: &[Option<Inst>],
        funcs: &mut FuncLookup,
        code_map: &CodeMap,
        frame: FrameView,
    ) -> Option<FrameEntry> {
        let ip = frame.ip_addr();
        let expected = frame
            .instance()
            .and_then(|instance| entities.iter().position(|&entity| entity == Some(instance)));
        // Note: we fall back to searching all instances in case the instance of the frame
        //       is unknown or does not match with the function executed by the frame.
        let (instance, func) = expected
            .into_iter()
            .chain(0..self.instances.len())
            .find_map(|instance| {
                let module = self.instances[instance].module;
                let func = funcs.find(code_map, module, &self.modules[module as usize], ip)?;
                Some((instance, func))
            })?;
        let module = &self.modules[self.instances[instance].module as usize].module;
        let func_index = module.get_func_index(func)?.into_u32();
        let locals = read_locals(frame.cells(), funcs.local_types(code_map, func));
        Some(FrameEntry {
            instance: instance as u32,
            func: func_index,
            locals,
        })
    }

    /// Records all initialized instances of `store` that have not yet been recorded.
    fn push_instances(&mut self, store: &StoreInner) {
        for (instance, entity) in store.instances() {
            if !entity.is_initialized() || self.contains_instance(&instance) {
                continue;
            }
            let Some(module) = entity.module() else {
                continue;
            };
            let module = self.push_module(module);
            let memories = entity
                .memories()
                .iter()
                .map(|memory| self.push_memory(store, memory))
                .collect();
            let globals = entity
                .globals()
                .iter()
                .map(|global| self.push_global(store, global))
                .collect();
            self.instances.push(InstanceEntry {
                instance,
                module,
                memories,
                globals,
            });
        }
    }

    /// Returns `true` if `instance` has already been recorded.
    fn contains_instance(&self, instance: &Instance) -> bool {
        self.instances
            .iter()
            .any(|entry| entry.instance.as_raw() == instance.as_raw())
    }

    /// Records `module` if necessary and returns its index within [`Contents::modules`].
    fn push_module(&mut self, module: &Module) -> u32 {
        if let Some(index) = self
            .modules
            .iter()
            .position(|entry| Module::same(&entry.module, module))
        {
            return index as u32;
        }
        self.modules.push(ModuleEntry {
            module: module.clone(),
            name: module_name(module).into(),
        });
        (self.modules.len() - 1) as u32
    }

    /// Records `memory` if necessary and returns its index within [`Contents::memories`].
    fn push_memory(&mut self, store: &StoreInner, memory: &Memory) -> u32 {
        if let Some(index) = self
            .memories
            .iter()
            .position(|entry| entry.memory.as_raw() == memory.as_raw())
        {
            return index as u32;
        }
        let entity = store.resolve_memory(memory);
        self.memories.push(MemoryEntry {
            memory: *memory,
            ty: entity.dynamic_ty(),
            data: MemoryData::Snapshot(entity.data().into()),
        });
        (self.memories.len() - 1) as u32
    }

    /// Records `global` if necessary and returns its index within [`Contents::globals`].
    fn push_global(&mut self, store: &StoreInner, global: &Global) -> u32 {
        if let Some(index) = self
            .globals
            .iter()
            .position(|entry| entry.global.as_raw() == global.as_raw())
        {
            return index as u32;
        }
        let entity = store.resolve_global(global);
        self.globals.push(GlobalEntry {
            global: *global,
            ty: entity.ty(),
            value: *entity.get_raw(),
        });
        (self.globals.len() - 1) as u32
    }
}

/// Reads the values of locals with the given `local_types` from the `cells` of their frame.
///
/// Values that are out of bounds of `cells` are [`Value::Missing`].
fn read_locals(cells: &[Cell], local_types: &[ValType]) -> Box<[Value]> {
    let mut offset = 0;
    local_types
        .iter()
        .map(|&ty| {
            let value = cells
                .get(offset)
                .map_or(Value::Missing, |&cell| Value::from_cell(cell, ty));
            offset += usize::from(required_cells_for_ty(ty));
            value
        })
        .collect()
}

/// Returns the name of `module` stored in its Wasm `name` custom section if any.
///
/// Returns an empty name otherwise.
fn module_name(module: &Module) -> &str {
    module
        .custom_sections()
        .filter(|section| section.name() == "name")
        .flat_map(|section| {
            NameSectionReader::new(BinaryReader::new(section.data(), 0)).map_while(Result::ok)
        })
        .find_map(|name| match name {
            Name::Module { name, .. } => Some(name),
            _ => None,
        })
        .unwrap_or_default()
}

/// Caches lookups of compiled functions during frame recording.
#[derive(Default)]
struct FuncLookup {
    /// The sorted address ranges of the compiled functions of the recorded modules by module index.
    ranges: BTreeMap<u32, Vec<(Range<usize>, EngineFunc)>>,
    /// The parameter and local types of the looked up compiled functions.
    local_types: BTreeMap<EngineFunc, Box<[ValType]>>,
}

impl FuncLookup {
    /// Returns the compiled function of `module` whose code contains the address `ip` if any.
    fn find(
        &mut self,
        code_map: &CodeMap,
        module_index: u32,
        module: &ModuleEntry,
        ip: usize,
    ) -> Option<EngineFunc> {
        let ranges = self
            .ranges
            .entry(module_index)
            .or_insert_with(|| code_map.compiled_ops_ranges(module.module.engine_funcs()));
        let index = ranges
            .partition_point(|(ops, _)| ops.start <= ip)
            .checked_sub(1)?;
        let (ops, func) = &ranges[index];
        (ip <= ops.end).then_some(*func)
    }

    /// Returns the parameter and local types of the compiled `func`.
    fn local_types(&mut self, code_map: &CodeMap, func: EngineFunc) -> &[ValType] {
        self.local_types
            .entry(func)
            .or_insert_with(|| code_map.local_types(func).unwrap_or_default())
    }
}
