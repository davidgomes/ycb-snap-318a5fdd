from dataclasses import dataclass

import pytest
from dirty_equals import IsInstance
from tests_helpers.misc import raises_exc_text

from adaptix import DebugTrail, NameStyle, Retort, name_mapping
from adaptix._internal.definitions import Direction
from adaptix._internal.morphing.facade.func import generate_json_schema
from adaptix._internal.morphing.model.crown_definitions import (
    ExtraForbid,
    ExtraSkip,
    InpDictCrown,
    InpFieldCrown,
    InpListCrown,
    InputNameLayout,
)
from tests.unit.morphing.name_layout.test_provider import DEFAULT_NAME_MAPPING, Layouts, make_layouts, TestField
from adaptix.load_error import AggregateLoadError, ExtraFieldsLoadError, TypeLoadError


@dataclass
class Book:
    title: str
    author: str = ""


def test_load_via_explicit_alias():
    retort = Retort(
        recipe=[
            name_mapping(
                aliases={"title": "name"},
            ),
        ],
    )
    assert retort.load({"name": "Dune"}, Book) == Book(title="Dune")


def test_primary_and_alias_together_raises():
    retort = Retort(
        recipe=[
            name_mapping(
                aliases={"title": "name"},
            ),
        ],
    )
    with pytest.raises(AggregateLoadError) as exc_info:
        retort.load({"title": "Primary", "name": "Alias"}, Book)
    assert isinstance(exc_info.value.exceptions[0], ExtraFieldsLoadError)
    assert set(exc_info.value.exceptions[0].fields) == {"title", "name"}


def test_multi_key_conflict_raises():
    retort = Retort(
        recipe=[
            name_mapping(
                aliases={"title": ["name", "caption"]},
            ),
        ],
    )
    with pytest.raises(AggregateLoadError) as exc_info:
        retort.load({"name": "A", "caption": "B"}, Book)
    assert isinstance(exc_info.value.exceptions[0], ExtraFieldsLoadError)
    assert set(exc_info.value.exceptions[0].fields) == {"name", "caption"}


def test_alias_not_used_on_dump():
    retort = Retort(
        recipe=[
            name_mapping(
                aliases={"title": "name"},
            ),
        ],
    )
    dumped = retort.dump(Book(title="Dune"))
    assert "name" not in dumped
    assert dumped["title"] == "Dune"


@dataclass
class CamelBook:
    book_title: str


def test_alias_style_generates_aliases():
    retort = Retort(
        recipe=[
            name_mapping(
                alias_style=NameStyle.CAMEL,
            ),
        ],
    )
    assert retort.load({"bookTitle": "Dune"}, CamelBook) == CamelBook(book_title="Dune")


def test_explicit_alias_equal_to_primary_errors():
    raises_exc_text(
        lambda: Retort(
            recipe=[
                name_mapping(
                    aliases={"title": "title"},
                ),
            ],
        ).get_loader(Book),
        """
        adaptix.ProviderNotFoundError: Cannot produce loader for type <class 'tests.unit.morphing.name_layout.test_aliases.Book'>
          × Cannot create loader for model. Cannot fetch `InputNameLayout`
          │ Location: ‹Book›
          ╰──▷ Explicit alias must not be equal to the field primary key
             ╰──▷ Field 'title' has explicit alias 'title' equal to its primary key 'title'
        """,
        {"Book": Book.__qualname__},
    )


def test_cross_field_alias_collision_errors():
    raises_exc_text(
        lambda: Retort(
            recipe=[
                name_mapping(
                    aliases={"title": "author"},
                ),
            ],
        ).get_loader(Book),
        """
        adaptix.ProviderNotFoundError: Cannot produce loader for type <class 'tests.unit.morphing.name_layout.test_aliases.Book'>
          × Cannot create loader for model. Cannot fetch `InputNameLayout`
          │ Location: ‹Book›
          ╰──▷ Invalid field aliases
             ╰──▷ Alias 'author' of field 'title' collides with primary key of another field
        """,
        {"Book": Book.__qualname__},
    )


def test_aliases_ignored_under_as_list():
    layouts = make_layouts(
        TestField("a"),
        name_mapping(
            as_list=True,
            aliases={"a": "alias_a"},
        ),
        DEFAULT_NAME_MAPPING,
    )
    assert isinstance(layouts.inp.crown, InpListCrown)
    assert layouts.inp.crown.map == (InpFieldCrown("a"),)


def test_extra_forbid_treats_aliases_as_known():
    retort = Retort(
        recipe=[
            name_mapping(
                aliases={"title": "name"},
                extra_in=ExtraForbid(),
            ),
        ],
    )
    retort.load({"name": "Dune"}, Book)
    with pytest.raises(AggregateLoadError):
        retort.load({"name": "Dune", "unknown": 1}, Book)


def test_extra_collect_does_not_collect_aliases():
    @dataclass
    class WithExtra:
        title: str

    def saturator(obj, extra):
        obj.extra = extra

    retort = Retort(
        recipe=[
            name_mapping(
                aliases={"title": "name"},
                extra_in=saturator,
            ),
        ],
    )

    @dataclass
    class WithExtraField(WithExtra):
        extra: dict | None = None

    result = retort.load({"name": "Dune", "other": 1}, WithExtraField)
    assert result.title == "Dune"
    assert result.extra == {"other": 1}


def test_trail_reflects_resolved_alias():
    retort = Retort(
        recipe=[
            name_mapping(
                aliases={"title": "name"},
            ),
        ],
    ).replace(debug_trail=DebugTrail.FIRST)

    with pytest.raises(TypeLoadError) as exc_info:
        retort.load({"name": 123}, Book)
    assert exc_info.value.__notes__
    assert "name" in exc_info.value.__notes__[0]


def test_input_layout_includes_field_key_groups():
    layouts = make_layouts(
        TestField("a"),
        name_mapping(
            aliases={"a": "alias_a"},
        ),
        DEFAULT_NAME_MAPPING,
    )
    assert layouts == Layouts(
        InputNameLayout(
            crown=InpDictCrown(
                map={"a": InpFieldCrown("a")},
                extra_policy=ExtraSkip(),
                field_key_groups={"a": ("a", "alias_a")},
            ),
            extra_move=IsInstance(type(None)),
        ),
        IsInstance(object),
    )


def test_overlay_merge_first_wins_per_field():
    retort = Retort(
        recipe=[
            name_mapping(
                aliases={"title": "first_alias"},
            ),
            name_mapping(
                aliases={"title": "second_alias"},
            ),
        ],
    )
    assert retort.load({"first_alias": "Dune"}, Book) == Book(title="Dune")
    with pytest.raises(AggregateLoadError):
        retort.load({"second_alias": "Dune"}, Book)


def test_generated_alias_matching_primary_is_pruned():
    retort = Retort(
        recipe=[
            name_mapping(
                alias_style=NameStyle.LOWER_SNAKE,
                aliases={"title": "other"},
            ),
        ],
    )
    assert retort.load({"other": "Dune"}, Book) == Book(title="Dune")
    with pytest.raises(AggregateLoadError):
        retort.load({"title": "Dune", "other": "X"}, Book)


def test_input_json_schema_includes_aliases():
    retort = Retort(
        recipe=[
            name_mapping(
                aliases={"title": "name"},
            ),
        ],
    )
    schema = generate_json_schema(retort, Book, direction=Direction.INPUT)
    book_schema = schema["$defs"][f"<class '{Book.__module__}.Book'>"]
    assert "name" in book_schema["properties"]
    assert "title" in book_schema["properties"]
    assert book_schema["properties"]["name"] == book_schema["properties"]["title"]
