from __future__ import annotations

from collections import Counter, deque
from collections.abc import Callable, Iterable
from collections.abc import Mapping as AbcMapping
from collections.abc import MutableMapping as AbcMutableMapping
from dataclasses import Field
from enum import Enum
from inspect import Signature
from inspect import signature as inspect_signature
from pathlib import Path
from typing import Any, Optional, Tuple, TypeVar, overload

from attrs import NOTHING, Attribute, resolve_types
from attrs import has as attrs_has
from typing_extensions import Self

from ._compat import (
    ANIES,
    FrozenSetSubscriptable,
    Mapping,
    MutableMapping,
    MutableSequence,
    NoneType,
    OriginAbstractSet,
    OriginMutableSet,
    Sequence,
    Set,
    TypeAlias,
    adapted_fields,
    fields,
    get_final_base,
    get_newtype_base,
    get_notrequired_base,
    get_origin,
    has,
    has_with_generic,
    is_annotated,
    is_bare,
    is_counter,
    is_deque,
    is_frozenset,
    is_generic,
    is_generic_attrs,
    is_hetero_tuple,
    is_literal,
    is_mapping,
    is_mutable_sequence,
    is_mutable_set,
    is_optional,
    is_protocol,
    is_subclass,
    is_tuple,
    is_typeddict,
    is_union_type,
    signature,
)
from .cols import (
    defaultdict_structure_factory,
    homogenous_tuple_structure_factory,
    is_abstract_set,
    is_defaultdict,
    is_namedtuple,
    is_sequence,
    iterable_unstructure_factory,
    list_structure_factory,
    mapping_structure_factory,
    mapping_unstructure_factory,
    namedtuple_structure_factory,
    namedtuple_unstructure_factory,
)
from .disambiguators import create_default_dis_func, is_supported_union
from .dispatch import (
    HookFactory,
    MultiStrategyDispatch,
    StructuredValue,
    StructureHook,
    TargetType,
    UnstructuredValue,
    UnstructureHook,
)
from .enums import enum_structure_factory, enum_unstructure_factory
from .errors import (
    AttributeValidationNote,
    ClassValidationError,
    ForbiddenExtraKeysError,
    IterableValidationError,
    IterableValidationNote,
    StructureHandlerNotFoundError,
)
from .fns import Predicate, identity, raise_error
from .gen import (
    AttributeOverride,
    HeteroTupleUnstructureFn,
    IterableUnstructureFn,
    MappingUnstructureFn,
    make_dict_structure_fn,
    make_dict_unstructure_fn,
    make_hetero_tuple_unstructure_fn,
)
from .gen.typeddicts import _adapted_fields, _required_keys
from .gen.typeddicts import make_dict_structure_fn as make_typeddict_dict_struct_fn
from .gen.typeddicts import make_dict_unstructure_fn as make_typeddict_dict_unstruct_fn
from .literals import is_literal_containing_enums
from .partial import PartialResult
from .typealiases import (
    get_type_alias_base,
    is_type_alias,
    type_alias_structure_factory,
)
from .types import SimpleStructureHook

__all__ = [
    "BaseConverter",
    "Converter",
    "GenConverter",
    "PartialResult",
    "UnstructureStrategy",
]

T = TypeVar("T")
V = TypeVar("V")

UnstructureHookFactory = TypeVar(
    "UnstructureHookFactory", bound=HookFactory[UnstructureHook]
)

# The Extended factory also takes a converter.
ExtendedUnstructureHookFactory: TypeAlias = Callable[[TargetType, T], UnstructureHook]

# This typevar for the BaseConverter.
AnyUnstructureHookFactoryBase = TypeVar(
    "AnyUnstructureHookFactoryBase",
    bound="HookFactory[UnstructureHook] | ExtendedUnstructureHookFactory[BaseConverter]",
)

# This typevar for the Converter.
AnyUnstructureHookFactory = TypeVar(
    "AnyUnstructureHookFactory",
    bound="HookFactory[UnstructureHook] | ExtendedUnstructureHookFactory[Converter]",
)

StructureHookFactory = TypeVar("StructureHookFactory", bound=HookFactory[StructureHook])

# The Extended factory also takes a converter.
ExtendedStructureHookFactory: TypeAlias = Callable[[TargetType, T], StructureHook]

# This typevar for the BaseConverter.
AnyStructureHookFactoryBase = TypeVar(
    "AnyStructureHookFactoryBase",
    bound="HookFactory[StructureHook] | ExtendedStructureHookFactory[BaseConverter]",
)

# This typevar for the Converter.
AnyStructureHookFactory = TypeVar(
    "AnyStructureHookFactory",
    bound="HookFactory[StructureHook] | ExtendedStructureHookFactory[Converter]",
)

UnstructureHookT = TypeVar("UnstructureHookT", bound=UnstructureHook)
StructureHookT = TypeVar("StructureHookT", bound=StructureHook)
CounterT = TypeVar("CounterT", bound=Counter)


class UnstructureStrategy(Enum):
    """`attrs` classes unstructuring strategies."""

    AS_DICT = "asdict"
    AS_TUPLE = "astuple"


def _is_extended_factory(factory: Callable) -> bool:
    """Does this factory also accept a converter arg?"""
    # We use the original `inspect.signature` to not evaluate string
    # annotations.
    sig = inspect_signature(factory)
    return (
        len(sig.parameters) >= 2
        and (list(sig.parameters.values())[1]).default is Signature.empty
    )


class BaseConverter:
    """Converts between structured and unstructured data."""

    __slots__ = (
        "_dict_factory",
        "_prefer_attrib_converters",
        "_struct_copy_skip",
        "_structure_attrs",
        "_structure_func",
        "_union_struct_registry",
        "_unstruct_copy_skip",
        "_unstructure_attrs",
        "_unstructure_func",
        "detailed_validation",
    )

    def __init__(
        self,
        dict_factory: Callable[[], Any] = dict,
        unstruct_strat: UnstructureStrategy = UnstructureStrategy.AS_DICT,
        prefer_attrib_converters: bool = False,
        detailed_validation: bool = True,
        unstructure_fallback_factory: HookFactory[UnstructureHook] = lambda _: identity,
        structure_fallback_factory: HookFactory[StructureHook] = lambda t: raise_error(
            None, t
        ),
    ) -> None:
        """
        :param detailed_validation: Whether to use a slightly slower mode for detailed
            validation errors.
        :param unstructure_fallback_factory: A hook factory to be called when no
            registered unstructuring hooks match.
        :param structure_fallback_factory: A hook factory to be called when no
            registered structuring hooks match.

        ..  versionadded:: 23.2.0 *unstructure_fallback_factory*
        ..  versionadded:: 23.2.0 *structure_fallback_factory*
        ..  versionchanged:: 24.2.0
            The default `structure_fallback_factory` now raises errors for missing handlers
            more eagerly, surfacing problems earlier.
        """
        unstruct_strat = UnstructureStrategy(unstruct_strat)
        self._prefer_attrib_converters = prefer_attrib_converters

        self.detailed_validation = detailed_validation
        self._union_struct_registry: dict[Any, Callable[[Any, type[T]], T]] = {}

        # Create a per-instance cache.
        if unstruct_strat is UnstructureStrategy.AS_DICT:
            self._unstructure_attrs = self.unstructure_attrs_asdict
            self._structure_attrs = self.structure_attrs_fromdict
        else:
            self._unstructure_attrs = self.unstructure_attrs_astuple
            self._structure_attrs = self.structure_attrs_fromtuple

        self._unstructure_func = MultiStrategyDispatch(
            unstructure_fallback_factory, self
        )
        self._unstructure_func.register_cls_list(
            [(bytes, identity), (str, identity), (Path, str)]
        )
        self._unstructure_func.register_func_list(
            [
                (
                    lambda t: get_newtype_base(t) is not None,
                    lambda o: self.unstructure(o, unstructure_as=o.__class__),
                ),
                (
                    is_protocol,
                    lambda o: self.unstructure(o, unstructure_as=o.__class__),
                ),
                (
                    lambda t: get_final_base(t) is not None,
                    lambda t: self.get_unstructure_hook(get_final_base(t)),
                    True,
                ),
                (
                    is_type_alias,
                    lambda t: self.get_unstructure_hook(get_type_alias_base(t)),
                    True,
                ),
                (is_mapping, self._unstructure_mapping),
                (is_sequence, self._unstructure_seq),
                (is_mutable_set, self._unstructure_seq),
                (is_frozenset, self._unstructure_seq),
                (is_literal_containing_enums, self.unstructure),
                (lambda t: is_subclass(t, Enum), enum_unstructure_factory, "extended"),
                (has, self._unstructure_attrs),
                (is_union_type, self._unstructure_union),
                (lambda t: t in ANIES, self.unstructure),
            ]
        )

        # Per-instance register of to-attrs converters.
        # Singledispatch dispatches based on the first argument, so we
        # store the function and switch the arguments in self.loads.
        self._structure_func = MultiStrategyDispatch(structure_fallback_factory, self)
        self._structure_func.register_func_list(
            [
                (
                    lambda cl: cl in ANIES or cl is Optional or cl is None,
                    lambda v, _: v,
                ),
                (is_generic_attrs, self._gen_structure_generic, True),
                (lambda t: get_newtype_base(t) is not None, self._structure_newtype),
                (is_type_alias, type_alias_structure_factory, "extended"),
                (
                    lambda t: get_final_base(t) is not None,
                    self._structure_final_factory,
                    True,
                ),
                (is_literal, self._structure_simple_literal),
                (is_literal_containing_enums, self._structure_enum_literal),
                (is_sequence, homogenous_tuple_structure_factory, "extended"),
                (is_mutable_sequence, list_structure_factory, "extended"),
                (is_deque, self._structure_deque),
                (is_mutable_set, self._structure_set),
                (is_abstract_set, self._structure_frozenset),
                (is_frozenset, self._structure_frozenset),
                (is_tuple, self._structure_tuple),
                (is_namedtuple, namedtuple_structure_factory, "extended"),
                (is_mapping, self._structure_dict),
                *(
                    [(is_supported_union, self._gen_attrs_union_structure, True)]
                    if unstruct_strat is UnstructureStrategy.AS_DICT
                    else []
                ),
                (is_optional, self._structure_optional),
                (
                    lambda t: is_union_type(t) and t in self._union_struct_registry,
                    self._union_struct_registry.__getitem__,
                    True,
                ),
                (lambda t: is_subclass(t, Enum), enum_structure_factory, "extended"),
                (has, self._structure_attrs),
            ]
        )
        # Strings are sequences.
        self._structure_func.register_cls_list(
            [
                (str, self._structure_call),
                (bytes, self._structure_call),
                (int, self._structure_call),
                (float, self._structure_call),
                (Path, self._structure_call),
            ]
        )

        self._dict_factory = dict_factory

        self._unstruct_copy_skip = self._unstructure_func.get_num_fns()
        self._struct_copy_skip = self._structure_func.get_num_fns()

    def unstructure(self, obj: Any, unstructure_as: Any = None) -> Any:
        return self._unstructure_func.dispatch(
            obj.__class__ if unstructure_as is None else unstructure_as
        )(obj)

    @property
    def unstruct_strat(self) -> UnstructureStrategy:
        """The default way of unstructuring ``attrs`` classes."""
        return (
            UnstructureStrategy.AS_DICT
            if self._unstructure_attrs == self.unstructure_attrs_asdict
            else UnstructureStrategy.AS_TUPLE
        )

    @overload
    def register_unstructure_hook(self, cls: UnstructureHookT) -> UnstructureHookT: ...

    @overload
    def register_unstructure_hook(self, cls: Any, func: UnstructureHook) -> None: ...

    def register_unstructure_hook(
        self, cls: Any = None, func: UnstructureHook | None = None
    ) -> Callable[[UnstructureHook]] | None:
        """Register a class-to-primitive converter function for a class.

        The converter function should take an instance of the class and return
        its Python equivalent.

        May also be used as a decorator. When used as a decorator, the first
        argument annotation from the decorated function will be used as the
        type to register the hook for.

        .. versionchanged:: 24.1.0
            This method may now be used as a decorator.
        .. versionchanged:: 25.1.0
            Modern type aliases are now supported.
        """
        if func is None:
            # Autodetecting decorator.
            func = cls
            sig = signature(func)
            cls = next(iter(sig.parameters.values())).annotation
            self.register_unstructure_hook(cls, func)

            return func

        if attrs_has(cls):
            resolve_types(cls)
        if is_union_type(cls):
            self._unstructure_func.register_func_list([(lambda t: t == cls, func)])
        elif is_type_alias(cls):
            self._unstructure_func.register_func_list([(lambda t: t is cls, func)])
        elif get_newtype_base(cls) is not None:
            # This is a newtype, so we handle it specially.
            self._unstructure_func.register_func_list([(lambda t: t is cls, func)])
        else:
            self._unstructure_func.register_cls_list([(cls, func)])
        return None

    def register_unstructure_hook_func(
        self, check_func: Predicate, func: UnstructureHook
    ) -> None:
        """Register a class-to-primitive converter function for a class, using
        a function to check if it's a match.
        """
        self._unstructure_func.register_func_list([(check_func, func)])

    @overload
    def register_unstructure_hook_factory(
        self, predicate: Predicate
    ) -> Callable[[AnyUnstructureHookFactoryBase], AnyUnstructureHookFactoryBase]: ...

    @overload
    def register_unstructure_hook_factory(
        self, predicate: Predicate, factory: UnstructureHookFactory
    ) -> UnstructureHookFactory: ...

    @overload
    def register_unstructure_hook_factory(
        self,
        predicate: Predicate,
        factory: ExtendedUnstructureHookFactory[BaseConverter],
    ) -> ExtendedUnstructureHookFactory[BaseConverter]: ...

    def register_unstructure_hook_factory(self, predicate, factory=None):
        """
        Register a hook factory for a given predicate.

        The hook factory may expose an additional required parameter. In this case,
        the current converter will be provided to the hook factory as that
        parameter.

        May also be used as a decorator.

        :param predicate: A function that, given a type, returns whether the factory
            can produce a hook for that type.
        :param factory: A callable that, given a type, produces an unstructuring
            hook for that type. This unstructuring hook will be cached.

        .. versionchanged:: 24.1.0
            This method may now be used as a decorator.
            The factory may also receive the converter as a second, required argument.
        """
        if factory is None:

            def decorator(factory):
                # Is this an extended factory (takes a converter too)?
                if _is_extended_factory(factory):
                    self._unstructure_func.register_func_list(
                        [(predicate, factory, "extended")]
                    )
                else:
                    self._unstructure_func.register_func_list(
                        [(predicate, factory, True)]
                    )

            return decorator

        self._unstructure_func.register_func_list(
            [
                (
                    predicate,
                    factory,
                    "extended" if _is_extended_factory(factory) else True,
                )
            ]
        )
        return factory

    def get_unstructure_hook(
        self, type: Any, cache_result: bool = True
    ) -> UnstructureHook:
        """Get the unstructure hook for the given type.

        This hook can be manually called, or composed with other functions
        and re-registered.

        If no hook is registered, the converter unstructure fallback factory
        will be used to produce one.

        :param cache: Whether to cache the returned hook.

        .. versionadded:: 24.1.0
        """
        return (
            self._unstructure_func.dispatch(type)
            if cache_result
            else self._unstructure_func.dispatch_without_caching(type)
        )

    @overload
    def register_structure_hook(self, cl: StructureHookT) -> StructureHookT: ...

    @overload
    def register_structure_hook(self, cl: Any, func: StructureHook) -> None: ...

    def register_structure_hook(
        self, cl: Any, func: StructureHook | None = None
    ) -> None:
        """Register a primitive-to-class converter function for a type.

        The converter function should take two arguments:
          * a Python object to be converted,
          * the type to convert to

        and return the instance of the class. The type may seem redundant, but
        is sometimes needed (for example, when dealing with generic classes).

        This method may be used as a decorator. In this case, the decorated
        hook must have a return type annotation, and this annotation will be used
        as the type for the hook.

        .. versionchanged:: 24.1.0
            This method may now be used as a decorator.
        .. versionchanged:: 25.1.0
            Modern type aliases are now supported.
        """
        if func is None:
            # The autodetecting decorator.
            func = cl
            sig = signature(func)
            self.register_structure_hook(sig.return_annotation, func)
            return func

        if attrs_has(cl):
            resolve_types(cl)
        if is_union_type(cl):
            self._union_struct_registry[cl] = func
            self._structure_func.clear_cache()
        elif is_type_alias(cl):
            # Type aliases are special-cased.
            self._structure_func.register_func_list([(lambda t: t is cl, func)])
        elif get_newtype_base(cl) is not None:
            # This is a newtype, so we handle it specially.
            self._structure_func.register_func_list([(lambda t: t is cl, func)])
        else:
            self._structure_func.register_cls_list([(cl, func)])
        return None

    def register_structure_hook_func(
        self, check_func: Predicate, func: StructureHook
    ) -> None:
        """Register a class-to-primitive converter function for a class, using
        a function to check if it's a match.
        """
        self._structure_func.register_func_list([(check_func, func)])

    @overload
    def register_structure_hook_factory(
        self, predicate: Predicate
    ) -> Callable[[AnyStructureHookFactoryBase], AnyStructureHookFactoryBase]: ...

    @overload
    def register_structure_hook_factory(
        self, predicate: Predicate, factory: StructureHookFactory
    ) -> StructureHookFactory: ...

    @overload
    def register_structure_hook_factory(
        self, predicate: Predicate, factory: ExtendedStructureHookFactory[BaseConverter]
    ) -> ExtendedStructureHookFactory[BaseConverter]: ...

    def register_structure_hook_factory(self, predicate, factory=None):
        """
        Register a hook factory for a given predicate.

        The hook factory may expose an additional required parameter. In this case,
        the current converter will be provided to the hook factory as that
        parameter.

        May also be used as a decorator.

        :param predicate: A function that, given a type, returns whether the factory
            can produce a hook for that type.
        :param factory: A callable that, given a type, produces a structuring
            hook for that type. This structuring hook will be cached.

        .. versionchanged:: 24.1.0
            This method may now be used as a decorator.
            The factory may also receive the converter as a second, required argument.
        """
        if factory is None:
            # Decorator use.
            def decorator(factory):
                # Is this an extended factory (takes a converter too)?
                if _is_extended_factory(factory):
                    self._structure_func.register_func_list(
                        [(predicate, factory, "extended")]
                    )
                else:
                    self._structure_func.register_func_list(
                        [(predicate, factory, True)]
                    )

            return decorator
        self._structure_func.register_func_list(
            [
                (
                    predicate,
                    factory,
                    "extended" if _is_extended_factory(factory) else True,
                )
            ]
        )
        return factory

    def structure(self, obj: UnstructuredValue, cl: type[T]) -> T:
        """Convert unstructured Python data structures to structured data."""
        return self._structure_func.dispatch(cl)(obj, cl)

    def partial_structure(self, obj: UnstructuredValue, cl: type[T]) -> PartialResult:
        """Structure *obj* into *cl*, keeping successful fields on failure.

        Returns a :class:`PartialResult`. Fields missing from the input are
        treated as failed (not structured). Failed fields with defaults are
        still applied as fallbacks when producing :attr:`PartialResult.value`.
        Required fields without defaults make ``value`` ``None``.

        Nested attrs classes, dataclasses and TypedDicts are structured
        recursively. Collection fields (lists, dicts, …) are structured
        atomically: any element failure fails the whole field.

        ``init=False`` fields are excluded from
        :attr:`PartialResult.structured_fields` and
        :attr:`PartialResult.failed_fields`.

        .. versionadded:: NEXT
        """
        return self._partial_structure_from(obj, cl, previous=None)

    def _partial_structure_from(
        self,
        obj: UnstructuredValue,
        cl: type[T],
        previous: PartialResult | None,
    ) -> PartialResult:
        origin = get_origin(cl) or cl
        if is_typeddict(cl) or is_typeddict(origin):
            return self._partial_structure_typeddict(obj, cl, previous)
        if has(cl) or has(origin):
            return self._partial_structure_class(obj, cl, previous)
        try:
            value = self.structure(obj, cl)
        except Exception as exc:
            return PartialResult(
                None,
                False,
                frozenset(),
                frozenset(),
                exc,
                {},
                self,
                cl,
            )
        return PartialResult(value, True, frozenset(), frozenset(), None, {}, self, cl)

    def _is_partialable_type(self, type_: Any) -> bool:
        if type_ is None:
            return False
        if is_optional(type_):
            union_params = type_.__args__
            other = (
                union_params[0] if union_params[1] is NoneType else union_params[1]
            )
            return self._is_partialable_type(other)
        origin = get_origin(type_) or type_
        return is_typeddict(type_) or is_typeddict(origin) or has(type_) or has(origin)

    def _unwrap_optional(self, type_: Any) -> Any:
        if not is_optional(type_):
            return type_
        union_params = type_.__args__
        return union_params[0] if union_params[1] is NoneType else union_params[1]

    def _partial_try_field(
        self,
        name: str,
        type_: Any,
        raw: Any,
        previous: PartialResult | None,
        note_cl: type,
        note_kind: str,
    ) -> tuple[Any | None, Exception | None, bool, PartialResult | None]:
        """Try to structure a single field.

        Returns ``(value, error, complete, nested_partial)``.
        ``complete`` is False when a nested object was only partially structured.
        """
        prev_nested = None if previous is None else previous._nested.get(name)
        target = self._unwrap_optional(type_)

        if (
            prev_nested is not None
            and isinstance(raw, AbcMapping)
            and self._is_partialable_type(target)
        ):
            nested = prev_nested.refine(raw)
            if nested.is_complete:
                return nested.value, None, True, None
            if nested.value is not None:
                return nested.value, nested.errors, False, nested
            err = nested.errors or KeyError(name)
            return None, err, False, nested

        if raw is None and is_optional(type_):
            return None, None, True, None

        if self._is_partialable_type(target) and isinstance(raw, AbcMapping):
            nested = self.partial_structure(raw, target)
            if nested.is_complete:
                return nested.value, None, True, None
            if nested.value is not None:
                return nested.value, nested.errors, False, nested
            err = nested.errors or KeyError(name)
            return None, err, False, nested

        try:
            return self._structure_func.dispatch(type_)(raw, type_), None, True, None
        except Exception as exc:
            if self.detailed_validation:
                msg = AttributeValidationNote(
                    f"Structuring {note_kind} {note_cl.__qualname__} @ attribute {name}",
                    name,
                    type_,
                )
                exc.__notes__ = [*getattr(exc, "__notes__", []), msg]
            return None, exc, False, None

    def _wrap_partial_errors(
        self, cl: type, errors: list[Exception]
    ) -> Exception | None:
        if not errors:
            return None
        if self.detailed_validation:
            return ClassValidationError(
                f"While structuring {cl.__name__!r}", errors, cl
            )
        return errors[0]

    def _missing_field_error(
        self, name: str, type_: Any, cl: type, note_kind: str
    ) -> Exception:
        exc: Exception = KeyError(name)
        if self.detailed_validation:
            msg = AttributeValidationNote(
                f"Structuring {note_kind} {cl.__qualname__} @ attribute {name}",
                name,
                type_,
            )
            exc.__notes__ = [*getattr(exc, "__notes__", []), msg]
        return exc

    def _partial_structure_class(
        self,
        obj: UnstructuredValue,
        cl: type[T],
        previous: PartialResult | None,
    ) -> PartialResult:
        origin = get_origin(cl) or cl
        attribs = adapted_fields(origin)
        use_alias = getattr(self, "use_alias", False)
        forbid_extra = getattr(self, "forbid_extra_keys", False)

        structured: set[str] = set()
        failed: set[str] = set()
        error_map: dict[str, Exception] = {}
        nested_map: dict[str, PartialResult] = {}
        extra_errors: list[Exception] = []
        conv_obj: dict[str, Any] = {}
        can_construct = True
        allowed_keys: set[str] = set()

        mapping = obj if isinstance(obj, AbcMapping) else None
        if mapping is None and previous is None:
            for a in attribs:
                if not a.init:
                    continue
                failed.add(a.name)
                error_map[a.name] = self._missing_field_error(
                    a.name, a.type, cl, "class"
                )
                if a.default is NOTHING:
                    can_construct = False
            errors = self._wrap_partial_errors(cl, list(error_map.values()))
            return PartialResult(
                None,
                False,
                frozenset(),
                frozenset(failed),
                errors,
                error_map,
                self,
                cl,
            )

        for a in attribs:
            if not a.init:
                continue
            kn = a.name if not use_alias else a.alias
            allowed_keys.add(kn)
            ctor_key = getattr(a, "alias", a.name)

            if previous is not None and a.name in previous.structured_fields:
                structured.add(a.name)
                if a.name in previous._values:
                    conv_obj[ctor_key] = previous._values[a.name]
                elif previous.value is not None:
                    conv_obj[ctor_key] = getattr(previous.value, a.name)
                continue

            raw_present = mapping is not None and kn in mapping
            if not raw_present:
                failed.add(a.name)
                error_map[a.name] = self._missing_field_error(
                    a.name, a.type, cl, "class"
                )
                if a.default is NOTHING:
                    can_construct = False
                continue

            raw = mapping[kn]
            value, err, complete, nested = self._partial_try_field(
                a.name, a.type, raw, previous, cl, "class"
            )
            if nested is not None:
                nested_map[a.name] = nested
            if complete and err is None:
                structured.add(a.name)
                conv_obj[ctor_key] = value
            else:
                failed.add(a.name)
                if err is not None:
                    error_map[a.name] = err
                if value is not None:
                    conv_obj[ctor_key] = value
                elif a.default is NOTHING:
                    can_construct = False

        if forbid_extra and mapping is not None:
            extra = set(mapping.keys()) - allowed_keys
            if extra:
                extra_errors.append(ForbiddenExtraKeysError("", cl, extra))

        inst = None
        if can_construct:
            try:
                inst = cl(**conv_obj)
            except Exception as exc:
                extra_errors.append(exc)
                inst = None

        collected = [*error_map.values(), *extra_errors]
        errors = self._wrap_partial_errors(cl, collected)
        is_complete = not failed and not extra_errors
        stored = dict(conv_obj)
        # Map constructor keys back to field names for refine.
        alias_to_name = {getattr(a, "alias", a.name): a.name for a in attribs if a.init}
        stored_by_name = {alias_to_name.get(k, k): v for k, v in stored.items()}
        return PartialResult(
            inst,
            is_complete,
            frozenset(structured),
            frozenset(failed),
            errors,
            error_map,
            self,
            cl,
            nested_map,
            stored_by_name,
        )

    def _partial_structure_typeddict(
        self,
        obj: UnstructuredValue,
        cl: type[T],
        previous: PartialResult | None,
    ) -> PartialResult:
        origin = get_origin(cl) or cl
        attribs = _adapted_fields(origin)
        req_keys = _required_keys(origin)
        forbid_extra = getattr(self, "forbid_extra_keys", False)

        structured: set[str] = set()
        failed: set[str] = set()
        error_map: dict[str, Exception] = {}
        nested_map: dict[str, PartialResult] = {}
        extra_errors: list[Exception] = []
        res: dict[str, Any] = {}
        can_construct = True
        allowed_keys: set[str] = set()

        mapping = obj if isinstance(obj, AbcMapping) else None
        if mapping is None and previous is None:
            for a in attribs:
                failed.add(a.name)
                error_map[a.name] = self._missing_field_error(
                    a.name, a.type, cl, "typeddict"
                )
                if a.name in req_keys:
                    can_construct = False
            errors = self._wrap_partial_errors(cl, list(error_map.values()))
            return PartialResult(
                None if not can_construct else {},
                False,
                frozenset(),
                frozenset(failed),
                errors,
                error_map,
                self,
                cl,
            )

        for a in attribs:
            an = a.name
            t = a.type
            nrb = get_notrequired_base(t)
            if nrb is not NOTHING:
                t = nrb
            allowed_keys.add(an)
            required = an in req_keys

            if previous is not None and an in previous.structured_fields:
                structured.add(an)
                if an in previous._values:
                    res[an] = previous._values[an]
                elif previous.value is not None and an in previous.value:
                    res[an] = previous.value[an]
                continue

            raw_present = mapping is not None and an in mapping
            if not raw_present:
                failed.add(an)
                error_map[an] = self._missing_field_error(an, t, cl, "typeddict")
                if required:
                    can_construct = False
                continue

            raw = mapping[an]
            value, err, complete, nested = self._partial_try_field(
                an, t, raw, previous, cl, "typeddict"
            )
            if nested is not None:
                nested_map[an] = nested
            if complete and err is None:
                structured.add(an)
                res[an] = value
            else:
                failed.add(an)
                if err is not None:
                    error_map[an] = err
                if value is not None:
                    res[an] = value
                elif required:
                    can_construct = False

        if forbid_extra and mapping is not None:
            extra = set(mapping.keys()) - allowed_keys
            if extra:
                extra_errors.append(ForbiddenExtraKeysError("", cl, extra))

        collected = [*error_map.values(), *extra_errors]
        errors = self._wrap_partial_errors(cl, collected)
        is_complete = not failed and not extra_errors
        return PartialResult(
            res if can_construct else None,
            is_complete,
            frozenset(structured),
            frozenset(failed),
            errors,
            error_map,
            self,
            cl,
            nested_map,
            dict(res),
        )

    def get_structure_hook(self, type: Any, cache_result: bool = True) -> StructureHook:
        """Get the structure hook for the given type.

        This hook can be manually called, or composed with other functions
        and re-registered.

        If no hook is registered, the converter structure fallback factory
        will be used to produce one.

        :param cache: Whether to cache the returned hook.

        .. versionadded:: 24.1.0
        """
        return (
            self._structure_func.dispatch(type)
            if cache_result
            else self._structure_func.dispatch_without_caching(type)
        )

    # Classes to Python primitives.
    def unstructure_attrs_asdict(self, obj: Any) -> dict[str, Any]:
        """Our version of `attrs.asdict`, so we can call back to us."""
        attrs = fields(obj.__class__)
        dispatch = self._unstructure_func.dispatch
        rv = self._dict_factory()
        for a in attrs:
            name = a.name
            v = getattr(obj, name)
            rv[name] = dispatch(a.type or v.__class__)(v)
        return rv

    def unstructure_attrs_astuple(self, obj: Any) -> tuple[Any, ...]:
        """Our version of `attrs.astuple`, so we can call back to us."""
        attrs = fields(obj.__class__)
        dispatch = self._unstructure_func.dispatch
        res = []
        for a in attrs:
            name = a.name
            v = getattr(obj, name)
            res.append(dispatch(a.type or v.__class__)(v))
        return tuple(res)

    def _unstructure_seq(self, seq: Sequence[T]) -> Sequence[T]:
        """Convert a sequence to primitive equivalents."""
        # We can reuse the sequence class, so tuples stay tuples.
        dispatch = self._unstructure_func.dispatch
        return seq.__class__(dispatch(e.__class__)(e) for e in seq)

    def _unstructure_mapping(self, mapping: Mapping[T, V]) -> Mapping[T, V]:
        """Convert a mapping of attr classes to primitive equivalents."""

        # We can reuse the mapping class, so dicts stay dicts and OrderedDicts
        # stay OrderedDicts.
        dispatch = self._unstructure_func.dispatch
        return mapping.__class__(
            (dispatch(k.__class__)(k), dispatch(v.__class__)(v))
            for k, v in mapping.items()
        )

    # note: Use UnionType when 3.11 is released as
    # the behaviour of @final is changed. This would
    # affect how we can support UnionType in ._compat.py
    def _unstructure_union(self, obj: Any) -> Any:
        """
        Unstructure an object as a union.

        By default, just unstructures the instance.
        """
        return self._unstructure_func.dispatch(obj.__class__)(obj)

    # Python primitives to classes.

    def _gen_structure_generic(
        self, cl: type[T]
    ) -> SimpleStructureHook[Mapping[str, Any], T]:
        """Create and return a hook for structuring generics."""
        return make_dict_structure_fn(
            cl, self, _cattrs_prefer_attrib_converters=self._prefer_attrib_converters
        )

    def _gen_attrs_union_structure(
        self, cl: Any, use_literals: bool = True
    ) -> Callable[[Any, type[T]], type[T] | None]:
        """
        Generate a structuring function for a union of attrs classes (and maybe None).

        :param use_literals: Whether to consider literal fields.
        """
        dis_fn = self._get_dis_func(cl, use_literals=use_literals)
        has_none = NoneType in cl.__args__

        if has_none:

            def structure_attrs_union(obj, _) -> cl:
                if obj is None:
                    return None
                return self.structure(obj, dis_fn(obj))

        else:

            def structure_attrs_union(obj, _):
                return self.structure(obj, dis_fn(obj))

        return structure_attrs_union

    @staticmethod
    def _structure_call(obj: Any, cl: type[T]) -> Any:
        """Just call ``cl`` with the given ``obj``.

        This is just an optimization on the ``_structure_default`` case, when
        we know we can skip the ``if`` s. Use for ``str``, ``bytes``, ``enum``,
        etc.
        """
        return cl(obj)

    @staticmethod
    def _structure_simple_literal(val, type):
        if val not in type.__args__:
            raise Exception(f"{val} not in literal {type}")
        return val

    @staticmethod
    def _structure_enum_literal(val, type):
        vals = {(x.value if isinstance(x, Enum) else x): x for x in type.__args__}
        try:
            return vals[val]
        except KeyError:
            raise Exception(f"{val} not in literal {type}") from None

    def _structure_newtype(self, val: UnstructuredValue, type) -> StructuredValue:
        base = get_newtype_base(type)
        return self.get_structure_hook(base)(val, base)

    def _structure_final_factory(self, type):
        base = get_final_base(type)
        res = self.get_structure_hook(base)
        return lambda v, _, __base=base: res(v, __base)

    # Attrs classes.

    def structure_attrs_fromtuple(self, obj: tuple[Any, ...], cl: type[T]) -> T:
        """Load an attrs class from a sequence (tuple)."""
        conv_obj = []  # A list of converter parameters.
        for a, value in zip(fields(cl), obj):
            # We detect the type by the metadata.
            converted = self._structure_attribute(a, value)
            conv_obj.append(converted)

        return cl(*conv_obj)

    def _structure_attribute(self, a: Attribute | Field, value: Any) -> Any:
        """Handle an individual attrs attribute."""
        type_ = a.type
        attrib_converter = getattr(a, "converter", None)
        if self._prefer_attrib_converters and attrib_converter:
            # A attrib converter is defined on this attribute, and
            # prefer_attrib_converters is set to give these priority over registered
            # structure hooks. So, pass through the raw value, which attrs will flow
            # into the converter
            return value
        if type_ is None:
            # No type metadata.
            return value

        try:
            return self._structure_func.dispatch(type_)(value, type_)
        except StructureHandlerNotFoundError:
            if attrib_converter:
                # Return the original value and fallback to using an attrib converter.
                return value
            raise

    def structure_attrs_fromdict(self, obj: Mapping[str, Any], cl: type[T]) -> T:
        """Instantiate an attrs class from a mapping (dict)."""
        # For public use.

        conv_obj = {}  # Start with a fresh dict, to ignore extra keys.
        for a in fields(cl):
            try:
                val = obj[a.name]
            except KeyError:
                continue

            # try .alias and .name because this code also supports dataclasses!
            conv_obj[getattr(a, "alias", a.name)] = self._structure_attribute(a, val)

        return cl(**conv_obj)

    def _structure_deque(self, obj: Iterable[T], cl: Any) -> deque[T]:
        """Convert an iterable to a potentially generic deque."""
        if is_bare(cl) or cl.__args__[0] in ANIES:
            res = deque(obj)
        else:
            elem_type = cl.__args__[0]
            handler = self._structure_func.dispatch(elem_type)
            if self.detailed_validation:
                errors = []
                res = deque()
                ix = 0  # Avoid `enumerate` for performance.
                for e in obj:
                    try:
                        res.append(handler(e, elem_type))
                    except Exception as e:
                        msg = IterableValidationNote(
                            f"Structuring {cl} @ index {ix}", ix, elem_type
                        )
                        e.__notes__ = [*getattr(e, "__notes__", []), msg]
                        errors.append(e)
                    finally:
                        ix += 1
                if errors:
                    raise IterableValidationError(
                        f"While structuring {cl!r}", errors, cl
                    )
            else:
                res = deque(handler(e, elem_type) for e in obj)
        return res

    def _structure_set(
        self, obj: Iterable[T], cl: Any, structure_to: type = set
    ) -> Set[T]:
        """Convert an iterable into a potentially generic set."""
        if is_bare(cl) or cl.__args__[0] in ANIES:
            return structure_to(obj)
        elem_type = cl.__args__[0]
        handler = self._structure_func.dispatch(elem_type)
        if self.detailed_validation:
            errors = []
            res = set()
            ix = 0
            for e in obj:
                try:
                    res.add(handler(e, elem_type))
                except Exception as exc:
                    msg = IterableValidationNote(
                        f"Structuring {structure_to.__name__} @ element {e!r}",
                        ix,
                        elem_type,
                    )
                    exc.__notes__ = [*getattr(exc, "__notes__", []), msg]
                    errors.append(exc)
                finally:
                    ix += 1
            if errors:
                raise IterableValidationError(f"While structuring {cl!r}", errors, cl)
            return res if structure_to is set else structure_to(res)
        if structure_to is set:
            return {handler(e, elem_type) for e in obj}
        return structure_to([handler(e, elem_type) for e in obj])

    def _structure_frozenset(
        self, obj: Iterable[T], cl: Any
    ) -> FrozenSetSubscriptable[T]:
        """Convert an iterable into a potentially generic frozenset."""
        return self._structure_set(obj, cl, structure_to=frozenset)

    def _structure_dict(self, obj: Mapping[T, V], cl: Any) -> dict[T, V]:
        """Convert a mapping into a potentially generic dict."""
        if is_bare(cl) or cl.__args__ == (Any, Any):
            return dict(obj)
        key_type, val_type = cl.__args__

        if self.detailed_validation:
            key_handler = self._structure_func.dispatch(key_type)
            val_handler = self._structure_func.dispatch(val_type)
            errors = []
            res = {}

            for k, v in obj.items():
                try:
                    value = val_handler(v, val_type)
                except Exception as exc:
                    msg = IterableValidationNote(
                        f"Structuring mapping value @ key {k!r}", k, val_type
                    )
                    exc.__notes__ = [*getattr(exc, "__notes__", []), msg]
                    errors.append(exc)
                    continue

                try:
                    key = key_handler(k, key_type)
                    res[key] = value
                except Exception as exc:
                    msg = IterableValidationNote(
                        f"Structuring mapping key @ key {k!r}", k, key_type
                    )
                    exc.__notes__ = [*getattr(exc, "__notes__", []), msg]
                    errors.append(exc)

            if errors:
                raise IterableValidationError(f"While structuring {cl!r}", errors, cl)
            return res

        if key_type in ANIES:
            val_conv = self._structure_func.dispatch(val_type)
            return {k: val_conv(v, val_type) for k, v in obj.items()}
        if val_type in ANIES:
            key_conv = self._structure_func.dispatch(key_type)
            return {key_conv(k, key_type): v for k, v in obj.items()}
        key_conv = self._structure_func.dispatch(key_type)
        val_conv = self._structure_func.dispatch(val_type)
        return {key_conv(k, key_type): val_conv(v, val_type) for k, v in obj.items()}

    def _structure_optional(self, obj, union):
        if obj is None:
            return None
        union_params = union.__args__
        other = union_params[0] if union_params[1] is NoneType else union_params[1]
        # We can't actually have a Union of a Union, so this is safe.
        return self._structure_func.dispatch(other)(obj, other)

    def _structure_tuple(self, obj: Iterable, tup: type[T]) -> T:
        """Deal with structuring into a tuple."""
        tup_params = None if tup in (Tuple, tuple) else tup.__args__
        has_ellipsis = tup_params and tup_params[-1] is Ellipsis
        if tup_params is None or (has_ellipsis and tup_params[0] in ANIES):
            # Just a Tuple. (No generic information.)
            return tuple(obj)
        if has_ellipsis:
            # We're dealing with a homogenous tuple, tuple[int, ...]
            tup_type = tup_params[0]
            conv = self._structure_func.dispatch(tup_type)
            if self.detailed_validation:
                errors = []
                res = []
                ix = 0
                for e in obj:
                    try:
                        res.append(conv(e, tup_type))
                    except Exception as exc:
                        msg = IterableValidationNote(
                            f"Structuring {tup} @ index {ix}", ix, tup_type
                        )
                        exc.__notes__ = [*getattr(exc, "__notes__", []), msg]
                        errors.append(exc)
                    finally:
                        ix += 1
                if errors:
                    raise IterableValidationError(
                        f"While structuring {tup!r}", errors, tup
                    )
                return tuple(res)
            return tuple(conv(e, tup_type) for e in obj)

        # We're dealing with a heterogenous tuple.
        exp_len = len(tup_params)
        if self.detailed_validation:
            errors = []
            res = []
            for ix, (t, e) in enumerate(zip(tup_params, obj)):
                try:
                    conv = self._structure_func.dispatch(t)
                    res.append(conv(e, t))
                except Exception as exc:
                    msg = IterableValidationNote(
                        f"Structuring {tup} @ index {ix}", ix, t
                    )
                    exc.__notes__ = [*getattr(exc, "__notes__", []), msg]
                    errors.append(exc)
            if len(obj) != exp_len:
                problem = "Not enough" if len(res) < exp_len else "Too many"
                exc = ValueError(f"{problem} values in {obj!r} to structure as {tup!r}")
                msg = f"Structuring {tup}"
                exc.__notes__ = [*getattr(exc, "__notes__", []), msg]
                errors.append(exc)
            if errors:
                raise IterableValidationError(f"While structuring {tup!r}", errors, tup)
            return tuple(res)

        if len(obj) != exp_len:
            problem = "Not enough" if len(obj) < len(tup_params) else "Too many"
            raise ValueError(f"{problem} values in {obj!r} to structure as {tup!r}")
        return tuple(
            [self._structure_func.dispatch(t)(e, t) for t, e in zip(tup_params, obj)]
        )

    def _get_dis_func(
        self,
        union: Any,
        use_literals: bool = True,
        overrides: dict[str, AttributeOverride] | None = None,
    ) -> Callable[[Any], type]:
        """Fetch or try creating a disambiguation function for a union."""
        union_types = union.__args__
        if NoneType in union_types:
            # We support unions of attrs classes and NoneType higher in the
            # logic.
            union_types = tuple(e for e in union_types if e is not NoneType)

        if not all(has(get_origin(e) or e) for e in union_types):
            raise StructureHandlerNotFoundError(
                "Only unions of attrs classes and dataclasses supported "
                "currently. Register a structure hook manually.",
                type_=union,
            )

        return create_default_dis_func(
            self,
            *union_types,
            use_literals=use_literals,
            overrides=overrides if overrides is not None else "from_converter",
        )

    def __deepcopy__(self, _) -> BaseConverter:
        return self.copy()

    def copy(
        self,
        dict_factory: Callable[[], Any] | None = None,
        unstruct_strat: UnstructureStrategy | None = None,
        prefer_attrib_converters: bool | None = None,
        detailed_validation: bool | None = None,
    ) -> Self:
        """Create a copy of the converter, keeping all existing custom hooks.

        :param detailed_validation: Whether to use a slightly slower mode for detailed
            validation errors.
        """
        res = self.__class__(
            dict_factory if dict_factory is not None else self._dict_factory,
            (
                unstruct_strat
                if unstruct_strat is not None
                else (
                    UnstructureStrategy.AS_DICT
                    if self._unstructure_attrs == self.unstructure_attrs_asdict
                    else UnstructureStrategy.AS_TUPLE
                )
            ),
            (
                prefer_attrib_converters
                if prefer_attrib_converters is not None
                else self._prefer_attrib_converters
            ),
            (
                detailed_validation
                if detailed_validation is not None
                else self.detailed_validation
            ),
        )

        self._unstructure_func.copy_to(res._unstructure_func, self._unstruct_copy_skip)
        self._structure_func.copy_to(res._structure_func, self._struct_copy_skip)

        return res


class Converter(BaseConverter):
    """A converter which generates specialized un/structuring functions."""

    __slots__ = (
        "_unstruct_collection_overrides",
        "forbid_extra_keys",
        "omit_if_default",
        "type_overrides",
        "use_alias",
    )

    def __init__(
        self,
        dict_factory: Callable[[], Any] = dict,
        unstruct_strat: UnstructureStrategy = UnstructureStrategy.AS_DICT,
        omit_if_default: bool = False,
        forbid_extra_keys: bool = False,
        type_overrides: Mapping[type, AttributeOverride] = {},
        unstruct_collection_overrides: Mapping[type, UnstructureHook] = {},
        prefer_attrib_converters: bool = False,
        detailed_validation: bool = True,
        unstructure_fallback_factory: HookFactory[UnstructureHook] = lambda _: identity,
        structure_fallback_factory: HookFactory[StructureHook] = lambda t: raise_error(
            None, t
        ),
        use_alias: bool = False,
    ):
        """
        :param detailed_validation: Whether to use a slightly slower mode for detailed
            validation errors.
        :param unstructure_fallback_factory: A hook factory to be called when no
            registered unstructuring hooks match.
        :param structure_fallback_factory: A hook factory to be called when no
            registered structuring hooks match.
        :param use_alias: Whether to use the field alias instead of the field name as
            the un/structured dictionary key by default.

        ..  versionadded:: 23.2.0 *unstructure_fallback_factory*
        ..  versionadded:: 23.2.0 *structure_fallback_factory*
        ..  versionchanged:: 24.2.0
            The default `structure_fallback_factory` now raises errors for missing handlers
            more eagerly, surfacing problems earlier.
        ..  versionadded:: 25.2.0 *use_alias*
        """
        super().__init__(
            dict_factory=dict_factory,
            unstruct_strat=unstruct_strat,
            prefer_attrib_converters=prefer_attrib_converters,
            detailed_validation=detailed_validation,
            unstructure_fallback_factory=unstructure_fallback_factory,
            structure_fallback_factory=structure_fallback_factory,
        )
        self.omit_if_default = omit_if_default
        self.forbid_extra_keys = forbid_extra_keys
        self.type_overrides = dict(type_overrides)
        self.use_alias = use_alias

        unstruct_collection_overrides = {
            get_origin(k) or k: v for k, v in unstruct_collection_overrides.items()
        }

        self._unstruct_collection_overrides = unstruct_collection_overrides

        # Do a little post-processing magic to make things easier for users.
        co = unstruct_collection_overrides

        # abc.Set overrides, if defined, apply to abc.MutableSets and sets
        if OriginAbstractSet in co:
            if OriginMutableSet not in co:
                co[OriginMutableSet] = co[OriginAbstractSet]
            if FrozenSetSubscriptable not in co:
                co[FrozenSetSubscriptable] = co[OriginAbstractSet]

        # abc.MutableSet overrrides, if defined, apply to sets
        if OriginMutableSet in co and set not in co:
            co[set] = co[OriginMutableSet]

        # abc.Sequence overrides, if defined, can apply to MutableSequences, lists and
        # tuples
        if Sequence in co:
            if MutableSequence not in co:
                co[MutableSequence] = co[Sequence]
            if tuple not in co:
                co[tuple] = co[Sequence]

        # abc.MutableSequence overrides, if defined, can apply to lists
        if MutableSequence in co:
            if list not in co:
                co[list] = co[MutableSequence]
            if deque not in co:
                co[deque] = co[MutableSequence]

        # abc.Mapping overrides, if defined, can apply to MutableMappings
        if Mapping in co and MutableMapping not in co:
            co[MutableMapping] = co[Mapping]

        # abc.MutableMapping overrides, if defined, can apply to dicts
        if MutableMapping in co and dict not in co:
            co[dict] = co[MutableMapping]

        # builtins.dict overrides, if defined, can apply to counters
        if dict in co and Counter not in co:
            co[Counter] = co[dict]

        if unstruct_strat is UnstructureStrategy.AS_DICT:
            # Override the attrs handler.
            self.register_unstructure_hook_factory(
                has_with_generic, self.gen_unstructure_attrs_fromdict
            )
            self.register_structure_hook_factory(
                has_with_generic, self.gen_structure_attrs_fromdict
            )
        self.register_unstructure_hook_factory(
            is_annotated, self.gen_unstructure_annotated
        )
        self.register_unstructure_hook_factory(
            is_hetero_tuple, self.gen_unstructure_hetero_tuple
        )
        self.register_unstructure_hook_factory(is_namedtuple)(
            namedtuple_unstructure_factory
        )
        self.register_unstructure_hook_factory(
            is_sequence, self.gen_unstructure_iterable
        )
        self.register_unstructure_hook_factory(is_mapping, self.gen_unstructure_mapping)
        self.register_unstructure_hook_factory(
            is_mutable_set,
            lambda cl: self.gen_unstructure_iterable(cl, unstructure_to=set),
        )
        self.register_unstructure_hook_factory(
            is_frozenset,
            lambda cl: self.gen_unstructure_iterable(cl, unstructure_to=frozenset),
        )
        self.register_unstructure_hook_factory(
            is_optional, self.gen_unstructure_optional
        )
        self.register_unstructure_hook_factory(
            is_typeddict, self.gen_unstructure_typeddict
        )
        self.register_unstructure_hook_factory(
            lambda t: get_newtype_base(t) is not None,
            lambda t: self.get_unstructure_hook(get_newtype_base(t)),
        )

        self.register_structure_hook_factory(is_annotated, self.gen_structure_annotated)
        self.register_structure_hook_factory(is_mapping, self.gen_structure_mapping)
        self.register_structure_hook_factory(is_counter, self.gen_structure_counter)
        self.register_structure_hook_factory(
            is_defaultdict, defaultdict_structure_factory
        )
        self.register_structure_hook_factory(is_typeddict, self.gen_structure_typeddict)
        self.register_structure_hook_factory(
            lambda t: get_newtype_base(t) is not None, self.get_structure_newtype
        )

        # We keep these so we can more correctly copy the hooks.
        self._struct_copy_skip = self._structure_func.get_num_fns()
        self._unstruct_copy_skip = self._unstructure_func.get_num_fns()

    @overload
    def register_unstructure_hook_factory(
        self, predicate: Predicate
    ) -> Callable[[AnyUnstructureHookFactory], AnyUnstructureHookFactory]: ...

    @overload
    def register_unstructure_hook_factory(
        self, predicate: Predicate, factory: UnstructureHookFactory
    ) -> UnstructureHookFactory: ...

    @overload
    def register_unstructure_hook_factory(
        self, predicate: Predicate, factory: ExtendedUnstructureHookFactory[Converter]
    ) -> ExtendedUnstructureHookFactory[Converter]: ...

    def register_unstructure_hook_factory(self, predicate, factory=None):
        # This dummy wrapper is required due to how `@overload` works.
        return super().register_unstructure_hook_factory(predicate, factory)

    @overload
    def register_structure_hook_factory(
        self, predicate: Predicate
    ) -> Callable[[AnyStructureHookFactory], AnyStructureHookFactory]: ...

    @overload
    def register_structure_hook_factory(
        self, predicate: Predicate, factory: StructureHookFactory
    ) -> StructureHookFactory: ...

    @overload
    def register_structure_hook_factory(
        self, predicate: Predicate, factory: ExtendedStructureHookFactory[Converter]
    ) -> ExtendedStructureHookFactory[Converter]: ...

    def register_structure_hook_factory(self, predicate, factory=None):
        # This dummy wrapper is required due to how `@overload` works.
        return super().register_structure_hook_factory(predicate, factory)

    def get_structure_newtype(self, type: type[T]) -> Callable[[Any, Any], T]:
        base = get_newtype_base(type)
        handler = self.get_structure_hook(base)
        return lambda v, _: handler(v, base)

    def gen_unstructure_annotated(self, type):
        origin = type.__origin__
        return self.get_unstructure_hook(origin)

    def gen_structure_annotated(self, type) -> Callable:
        """A hook factory for annotated types."""
        origin = type.__origin__
        hook = self.get_structure_hook(origin)
        return lambda v, _: hook(v, origin)

    def gen_unstructure_typeddict(self, cl: Any) -> Callable[[dict], dict]:
        """Generate a TypedDict unstructure function.

        Also apply converter-scored modifications.
        """
        return make_typeddict_dict_unstruct_fn(cl, self)

    def gen_unstructure_attrs_fromdict(
        self, cl: type[T]
    ) -> Callable[[T], dict[str, Any]]:
        origin = get_origin(cl)
        attribs = fields(origin or cl)
        if attrs_has(cl) and any(isinstance(a.type, str) for a in attribs):
            # PEP 563 annotations - need to be resolved.
            resolve_types(origin or cl)
        attrib_overrides = {
            a.name: self.type_overrides[a.type]
            for a in attribs
            if a.type in self.type_overrides
        }

        return make_dict_unstructure_fn(
            cl, self, _cattrs_omit_if_default=self.omit_if_default, **attrib_overrides
        )

    def gen_unstructure_optional(self, cl: type[T]) -> Callable[[T], Any]:
        """Generate an unstructuring hook for optional types."""
        union_params = cl.__args__
        other = union_params[0] if union_params[1] is NoneType else union_params[1]

        if isinstance(other, TypeVar):
            handler = self.unstructure
        else:
            handler = self.get_unstructure_hook(other)

        def unstructure_optional(val, _handler=handler):
            return None if val is None else _handler(val)

        return unstructure_optional

    def gen_structure_typeddict(self, cl: Any) -> Callable[[dict, Any], dict]:
        """Generate a TypedDict structure function.

        Also apply converter-scored modifications.
        """
        return make_typeddict_dict_struct_fn(
            cl, self, _cattrs_detailed_validation=self.detailed_validation
        )

    def gen_structure_attrs_fromdict(
        self, cl: type[T]
    ) -> Callable[[Mapping[str, Any], Any], T]:
        origin = get_origin(cl)
        attribs = fields(origin or cl if is_generic(cl) else cl)
        if attrs_has(cl) and any(isinstance(a.type, str) for a in attribs):
            # PEP 563 annotations - need to be resolved.
            resolve_types(origin or cl)
        attrib_overrides = {
            a.name: self.type_overrides[a.type]
            for a in attribs
            if a.type in self.type_overrides
        }
        return make_dict_structure_fn(
            cl,
            self,
            _cattrs_forbid_extra_keys=self.forbid_extra_keys,
            _cattrs_prefer_attrib_converters=self._prefer_attrib_converters,
            _cattrs_detailed_validation=self.detailed_validation,
            _cattrs_use_alias=self.use_alias,
            **attrib_overrides,
        )

    def gen_unstructure_iterable(
        self, cl: Any, unstructure_to: Any = None
    ) -> IterableUnstructureFn:
        unstructure_to = self._unstruct_collection_overrides.get(
            get_origin(cl) or cl, unstructure_to or list
        )
        h = iterable_unstructure_factory(cl, self, unstructure_to=unstructure_to)
        self._unstructure_func.register_cls_list([(cl, h)], direct=True)
        return h

    def gen_unstructure_hetero_tuple(
        self, cl: Any, unstructure_to: Any = None
    ) -> HeteroTupleUnstructureFn:
        unstructure_to = self._unstruct_collection_overrides.get(
            get_origin(cl) or cl, unstructure_to or tuple
        )
        h = make_hetero_tuple_unstructure_fn(cl, self, unstructure_to=unstructure_to)
        self._unstructure_func.register_cls_list([(cl, h)], direct=True)
        return h

    def gen_unstructure_mapping(
        self,
        cl: Any,
        unstructure_to: Any = None,
        key_handler: Callable[[Any, Any | None], Any] | None = None,
    ) -> MappingUnstructureFn:
        unstructure_to = self._unstruct_collection_overrides.get(
            get_origin(cl) or cl, unstructure_to or dict
        )
        h = mapping_unstructure_factory(
            cl, self, unstructure_to=unstructure_to, key_handler=key_handler
        )
        self._unstructure_func.register_cls_list([(cl, h)], direct=True)
        return h

    def gen_structure_counter(
        self, cl: type[CounterT]
    ) -> SimpleStructureHook[Mapping[Any, Any], CounterT]:
        h = mapping_structure_factory(
            cl,
            self,
            structure_to=Counter,
            val_type=int,
            detailed_validation=self.detailed_validation,
        )
        self._structure_func.register_cls_list([(cl, h)], direct=True)
        return h

    def gen_structure_mapping(
        self, cl: Any
    ) -> SimpleStructureHook[Mapping[Any, Any], Any]:
        structure_to = get_origin(cl) or cl
        if structure_to in (
            MutableMapping,
            AbcMutableMapping,
            Mapping,
            AbcMapping,
        ):  # These default to dicts
            structure_to = dict
        h = mapping_structure_factory(
            cl, self, structure_to, detailed_validation=self.detailed_validation
        )
        self._structure_func.register_cls_list([(cl, h)], direct=True)
        return h

    def copy(
        self,
        dict_factory: Callable[[], Any] | None = None,
        unstruct_strat: UnstructureStrategy | None = None,
        omit_if_default: bool | None = None,
        forbid_extra_keys: bool | None = None,
        type_overrides: Mapping[type, AttributeOverride] | None = None,
        unstruct_collection_overrides: Mapping[type, UnstructureHook] | None = None,
        prefer_attrib_converters: bool | None = None,
        detailed_validation: bool | None = None,
        use_alias: bool | None = None,
    ) -> Self:
        """Create a copy of the converter, keeping all existing custom hooks.

        :param detailed_validation: Whether to use a slightly slower mode for detailed
            validation errors.
        """
        res = self.__class__(
            dict_factory if dict_factory is not None else self._dict_factory,
            (
                unstruct_strat
                if unstruct_strat is not None
                else (
                    UnstructureStrategy.AS_DICT
                    if self._unstructure_attrs == self.unstructure_attrs_asdict
                    else UnstructureStrategy.AS_TUPLE
                )
            ),
            omit_if_default if omit_if_default is not None else self.omit_if_default,
            (
                forbid_extra_keys
                if forbid_extra_keys is not None
                else self.forbid_extra_keys
            ),
            type_overrides if type_overrides is not None else self.type_overrides,
            (
                unstruct_collection_overrides
                if unstruct_collection_overrides is not None
                else self._unstruct_collection_overrides
            ),
            (
                prefer_attrib_converters
                if prefer_attrib_converters is not None
                else self._prefer_attrib_converters
            ),
            (
                detailed_validation
                if detailed_validation is not None
                else self.detailed_validation
            ),
            use_alias=(use_alias if use_alias is not None else self.use_alias),
        )

        self._unstructure_func.copy_to(
            res._unstructure_func, skip=self._unstruct_copy_skip
        )
        self._structure_func.copy_to(res._structure_func, skip=self._struct_copy_skip)

        return res


GenConverter: TypeAlias = Converter
