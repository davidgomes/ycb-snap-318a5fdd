"""Per-state data ownership: lifecycle, scope, history, SCXML, and diagrams."""

import pickle

import pytest
from statemachine.contrib.diagram import DotGraphMachine
from statemachine.contrib.diagram import MermaidGraphMachine
from statemachine.data import format_data_item
from statemachine.exceptions import InvalidDefinition
from statemachine.io.scxml.parser import literals_from_data_items
from statemachine.io.scxml.parser import python_literal
from statemachine.io.scxml.processor import SCXMLProcessor
from statemachine.io.scxml.schema import DataItem

from statemachine import DataChangeInfo
from statemachine import DataVar
from statemachine import HistoryState
from statemachine import State
from statemachine import StateChart
from statemachine import StateMachine


class _Factory:
    def __call__(self):
        return []


def test_datavar_and_datachangeinfo_are_public():
    info = DataChangeInfo(state_id="a", key="n", old_value=0, new_value=1)
    assert info.state_id == "a"
    assert info.key == "n"
    assert info.old_value == 0
    assert info.new_value == 1
    assert "default=1" in repr(DataVar(default=1, type=int))


def test_invalid_declarations():
    with pytest.raises(InvalidDefinition, match="string keys"):
        State(data=["nope"])
    with pytest.raises(InvalidDefinition, match="string keys"):
        State(data={1: "x"})
    with pytest.raises(InvalidDefinition, match="both default and factory"):
        DataVar(default=1, factory=list)
    with pytest.raises(InvalidDefinition, match="callable"):
        DataVar(factory=1)  # type: ignore[arg-type]
    with pytest.raises(InvalidDefinition, match="type constraint"):
        DataVar(default=1, type=object()).validate("n", 1)


def test_format_data_item_variants():
    assert format_data_item("n", 1) == "n = 1"
    assert format_data_item("items", list) == "items = list()"
    assert format_data_item("items", _Factory()) == "items = factory()"
    assert format_data_item("n", DataVar()) == "n"
    assert format_data_item("n", DataVar(type=int)) == "n: int"
    assert format_data_item("n", DataVar(default=1)) == "n = 1"
    assert format_data_item("n", DataVar(default=1, type=int)) == "n: int = 1"
    assert format_data_item("n", DataVar(default=1, type=(int, str))) == "n: int | str = 1"
    assert format_data_item("items", DataVar(factory=list)) == "items = list()"
    assert format_data_item("items", DataVar(factory=list, type=list)) == "items: list = list()"


def test_python_literals_from_data_elements():
    assert python_literal("{'a': 1}") == {"a": 1}
    assert python_literal("1 + 2") is not None
    assert literals_from_data_items(
        [
            DataItem(id="count", src=None, expr="1", content=None),
            DataItem(id="label", src=None, expr="'hi'", content=None),
            DataItem(id="sum", src=None, expr="1 + 2", content=None),
            DataItem(id="blob", src=None, expr=None, content="hello"),
        ]
    ) == {"count": 1, "label": "hi"}
    assert literals_from_data_items([]) is None


class Counting(StateChart):
    a = State(initial=True, data={"n": 0, "items": [1], "box": DataVar(factory=dict, type=dict)})
    b = State(data={"n": DataVar(default=1, type=int), "label": "<b>"})
    go = a.to(b)
    back = b.to(a)

    def __init__(self, *args, **kwargs):
        self.exit_seen = None
        self.enter_seen = None
        super().__init__(*args, **kwargs)

    def on_exit_a(self, state_data, source, target, event_data):
        self.exit_seen = (dict(state_data), source.id, target.id, event_data.event)

    def on_enter_b(self, state_data):
        self.enter_seen = dict(state_data)
        state_data["n"] = 8


class SharedDefaults(StateChart):
    a = State(initial=True, final=True, data={"items": [1]})


class TypedEntry(StateChart):
    a = State(initial=True, final=True, data={"n": DataVar(type=int)})


class ValueKeyed(StateChart):
    a = State("Alpha", initial=True, value="alpha", data={"n": 1})
    b = State(final=True)
    go = a.to(b)


class ParentScope(StateChart):
    class parent(State.Compound, data={"shared": 1, "x": "parent"}):
        child = State(initial=True, final=True, data={"x": "child", "local": 2})

    def on_enter_child(self, state_data, source, target, event_data):
        state_data["x"] = "shadowed"
        state_data["shared"] = 7
        self.seen = dict(state_data)
        self.meta = (source.id, target.id, event_data)


class ParallelScope(StateChart):
    class zones(State.Parallel, data={"shared": "p"}):
        class left(State.Compound):
            a = State(initial=True, final=True, data={"x": 1, "only_a": "a"})

        class right(State.Compound):
            b = State(initial=True, final=True, data={"x": 2, "only_b": "b"})

    def on_enter_a(self, state_data):
        self.seen_a = dict(state_data)

    def on_enter_b(self, state_data):
        self.seen_b = dict(state_data)


class RootScope(StateChart):
    _root_state_data = {"global_n": 1}
    a = State(initial=True, final=True, data={"local": 2})

    def on_enter_a(self, state_data):
        self.seen = dict(state_data)


class Rollback(StateMachine):
    a = State(initial=True, data={"n": 0})
    b = State(final=True, data={"n": 1})
    go = a.to(b)

    def on_enter_b(self):
        raise RuntimeError("boom")


class Macrostep(StateChart):
    a = State(initial=True, data={"n": 0})
    b = State(data={"n": 1})
    c = State(final=True, data={"n": 2})
    go = a.to(b)
    b.to(c)

    def on_enter_b(self, state_data):
        state_data["n"] = 8

    def on_enter_c(self, state_data):
        state_data["n"] = 9


class DeepData(StateChart):
    class moria(State.Compound):
        class halls(State.Compound, data={"level": 0}):
            entrance = State(initial=True, data={"n": 0})
            chamber = State(data={"n": 1})
            explore = entrance.to(chamber)

        assert isinstance(halls, State)
        h = HistoryState(type="deep")  # type: ignore[has-type]
        bridge = State(final=True)
        flee = halls.to(bridge)

    outside = State()
    escape = moria.to(outside)
    return_deep = outside.to(moria.h)  # type: ignore[attr-defined, has-type]

    def on_exit_chamber(self, state_data):
        state_data["n"] = 40


class ShallowData(StateChart):
    class moria(State.Compound):
        class halls(State.Compound, data={"level": 0}):
            entrance = State(initial=True, data={"n": 0})
            chamber = State(data={"n": 1})
            explore = entrance.to(chamber)

        assert isinstance(halls, State)
        marker = State()
        h = HistoryState()  # type: ignore[has-type]
        bridge = State(final=True)
        flee = halls.to(bridge)
        side = halls.to(marker)

    outside = State()
    escape = moria.to(outside)
    return_shallow = outside.to(moria.h)  # type: ignore[attr-defined, has-type]

    def on_exit_halls(self, state_data):
        state_data["level"] = 11


class DiagramMachine(StateChart):
    class parent(State.Compound, data={"items": DataVar(factory=list, type=list)}):
        a = State(
            initial=True,
            final=True,
            data={"total": 0, "label": "<b>", "flag": DataVar(default=True, type=bool)},
        )


@pytest.mark.timeout(5)
class TestStateDataLifecycle:
    async def test_entry_exit_reset_and_isolation(self, sm_runner):
        sm = await sm_runner.start(Counting)
        assert sm.get_state_data("a")["n"] == 0
        assert sm.get_state_data(sm.a)["items"] == [1]
        sm.get_state_data("a")["items"].append(2)
        sm.set_state_data("a", "n", 5)
        other = Counting()

        await sm_runner.send(sm, "go")
        assert sm.exit_seen[0]["n"] == 5
        assert sm.exit_seen[0]["items"] == [1, 2]
        assert sm.exit_seen[1] == "a"
        assert sm.exit_seen[2] == "b"
        assert sm.get_state_data("a") is None
        assert sm.get_state_data(Counting.b)["n"] == 8
        assert sm.enter_seen["n"] == 1
        assert sm.enter_seen["label"] == "<b>"

        with pytest.raises(InvalidDefinition, match="not an active"):
            sm.set_state_data("a", "n", 1)
        with pytest.raises(InvalidDefinition, match="not a data key"):
            sm.set_state_data("b", "missing", 1)
        with pytest.raises(InvalidDefinition, match="not an instance"):
            sm.set_state_data(sm.b, "n", "x")
        assert sm.get_state_data("b")["n"] == 8

        await sm_runner.send(sm, "back")
        assert sm.get_state_data("a")["items"] == [1]
        assert sm.get_state_data("a")["box"] == {}
        assert sm.get_state_data("b") is None
        assert other.get_state_data("a")["items"] == [1]
        assert other.get_state_data("a")["n"] == 0

    async def test_instances_do_not_share_defaults(self, sm_runner):
        first = await sm_runner.start(SharedDefaults)
        second = await sm_runner.start(SharedDefaults)
        first.get_state_data("a")["items"].append(9)
        assert second.get_state_data("a")["items"] == [1]
        assert SharedDefaults.a.data == {"items": [1]}

    async def test_changes_follow_the_macrostep(self, sm_runner):
        sm = await sm_runner.start(Macrostep)
        sm.set_state_data("a", "n", 4)
        assert [(c.state_id, c.key, c.old_value, c.new_value) for c in sm.get_data_changes()] == [
            ("a", "n", 0, 4)
        ]
        await sm_runner.send(sm, "go")
        changes = sm.get_data_changes()
        assert [(c.state_id, c.key, c.new_value) for c in changes] == [
            ("b", "n", 8),
            ("c", "n", 9),
        ]
        assert sm.get_state_data("c")["n"] == 9
        assert sm.get_state_data("a") is None
        assert sm.get_state_data("b") is None
        # The returned list is a snapshot of the records.
        changes.append(DataChangeInfo("c", "n", 9, 9))
        assert len(sm.get_data_changes()) == 2

    async def test_values_are_a_snapshot(self, sm_runner):
        sm = await sm_runner.start(Counting)
        snapshot = sm.state_data_values
        sm.set_state_data("a", "n", 3)
        assert snapshot["a"]["n"] == 0
        assert sm.state_data_values["a"]["n"] == 3

    async def test_failed_transition_restores_data(self, sm_runner):
        sm = await sm_runner.start(Rollback)
        sm.set_state_data("a", "n", 5)
        sm._history_data["h"] = {"other": {"n": 1}}
        with pytest.raises(RuntimeError, match="boom"):
            await sm_runner.send(sm, "go")
        assert "a" in sm.configuration_values
        assert sm.get_state_data("a")["n"] == 5
        assert "b" not in sm.configuration_values
        assert sm._history_data["h"]["other"]["n"] == 1

    def test_datavar_type_is_checked_on_entry(self):
        with pytest.raises(InvalidDefinition, match="not an instance"):
            TypedEntry()

    async def test_lookup_by_id_value_and_object(self, sm_runner):
        sm = await sm_runner.start(ValueKeyed)
        assert sm.get_state_data("a")["n"] == 1
        assert sm.get_state_data("alpha")["n"] == 1
        assert sm.get_state_data(sm.a)["n"] == 1
        assert sm.get_state_data(ValueKeyed.a)["n"] == 1
        with pytest.raises(InvalidDefinition, match="not a state"):
            sm.get_state_data("missing")
        with pytest.raises(InvalidDefinition, match="Expected a state"):
            sm.get_state_data(1)  # type: ignore[arg-type]
        assert sm.scoped_state_data("alpha") == {"n": 1}
        assert (sm.scoped_state_data("a") == 1) is False

    async def test_hierarchy_shadows_and_parallel_regions_are_isolated(self, sm_runner):
        parent = await sm_runner.start(ParentScope)
        assert parent.seen == {"shared": 7, "x": "shadowed", "local": 2}
        assert parent.meta[0] == "parent"
        assert parent.meta[1] == "child"
        assert parent.get_state_data("parent")["x"] == "parent"
        assert parent.get_state_data("parent")["shared"] == 7
        assert parent.get_state_data("child")["x"] == "shadowed"
        assert "local" not in parent.scoped_state_data("parent")

        zones = await sm_runner.start(ParallelScope)
        assert zones.seen_a == {"shared": "p", "x": 1, "only_a": "a"}
        assert zones.seen_b == {"shared": "p", "x": 2, "only_b": "b"}
        assert "only_b" not in zones.seen_a
        assert zones.get_state_data("zones")["shared"] == "p"
        with pytest.raises(InvalidDefinition, match="not a declared"):
            zones.scoped_state_data("a")["only_b"] = "nope"

    async def test_root_data_is_visible_and_writable(self, sm_runner):
        sm = await sm_runner.start(RootScope)
        assert sm.seen == {"global_n": 1, "local": 2}
        sm.scoped_state_data("a")["global_n"] = 9
        assert sm.get_data_changes()[-1].state_id == "__root__"
        assert sm.get_data_changes()[-1].new_value == 9
        with pytest.raises(InvalidDefinition):
            del sm.scoped_state_data("a")["local"]
        with pytest.raises(InvalidDefinition, match="not a declared"):
            sm.scoped_state_data("a")["missing"] = 1
        other = await sm_runner.start(RootScope)
        assert other.get_state_data("a")["local"] == 2
        assert dict(other.scoped_state_data("a"))["global_n"] == 1

    async def test_deep_history_restores_descendant_data(self, sm_runner):
        sm = await sm_runner.start(DeepData)
        sm.set_state_data("halls", "level", 9)
        await sm_runner.send(sm, "explore")
        sm.set_state_data("chamber", "n", 8)
        await sm_runner.send(sm, "escape")
        assert sm.get_state_data("chamber") is None

        await sm_runner.send(sm, "return_deep")
        assert "chamber" in sm.configuration_values
        assert sm.get_state_data("chamber")["n"] == 40
        assert sm.get_state_data("halls")["level"] == 9
        assert sm.get_state_data("entrance") is None

    async def test_shallow_history_restores_only_direct_children(self, sm_runner):
        sm = await sm_runner.start(ShallowData)
        sm.set_state_data("halls", "level", 9)
        await sm_runner.send(sm, "explore")
        sm.set_state_data("chamber", "n", 8)
        await sm_runner.send(sm, "escape")
        await sm_runner.send(sm, "return_shallow")
        assert "entrance" in sm.configuration_values
        assert "chamber" not in sm.configuration_values
        assert sm.get_state_data("halls")["level"] == 11
        assert sm.get_state_data("entrance")["n"] == 0

    async def test_history_ignores_states_without_saved_data(self, sm_runner):
        sm = await sm_runner.start(ShallowData)
        await sm_runner.send(sm, "side")
        await sm_runner.send(sm, "escape")
        await sm_runner.send(sm, "return_shallow")
        assert "marker" in sm.configuration_values
        assert sm.get_state_data("marker") is None

    def test_pickle_preserves_active_data(self):
        sm = Counting()
        sm.set_state_data("a", "n", 5)
        sm.get_state_data("a")["items"].append(4)
        restored = pickle.loads(pickle.dumps(sm))
        assert restored.get_state_data("a")["n"] == 5
        assert restored.get_state_data("a")["items"] == [1, 4]
        restored.send("go")
        assert restored.get_state_data("b")["n"] == 8
        assert restored.get_state_data("a") is None

    def test_diagrams_annotate_data_variables(self):
        graph = MermaidGraphMachine(DiagramMachine).get_mermaid()
        assert "total = 0" in graph
        assert "label = '<b>'" in graph
        assert "flag: bool = True" in graph
        assert "items: list = list()" in graph

        dot = DotGraphMachine(DiagramMachine)().to_string()
        assert "total = 0" in dot
        assert "flag: bool = True" in dot
        assert "items: list = list()" in dot
        assert "&lt;b&gt;" in dot

    def test_scxml_data_elements_are_python_literals(self):
        scxml = """
        <scxml initial="s1">
          <datamodel>
            <data id="count" expr="1"/>
            <data id="label" expr="'hi'"/>
            <data id="items" expr="[1, 2]"/>
            <data id="calculated" expr="1 + 2"/>
          </datamodel>
          <state id="s1">
            <datamodel>
              <data id="local" expr="{'a': 1}"/>
            </datamodel>
            <transition event="go" target="s2"/>
          </state>
          <state id="s2">
            <datamodel>
              <data id="sum" expr="1 + 2"/>
            </datamodel>
            <transition event="back" target="s1"/>
          </state>
        </scxml>
        """
        processor = SCXMLProcessor()
        processor.parse_scxml("literal_data", scxml)
        machine_cls = processor.scs["literal_data"]
        assert machine_cls.s2.data is None
        sm = processor.start()
        assert sm.model.count == 1
        assert sm.model.calculated == 3
        assert sm.get_state_data("s1")["local"] == {"a": 1}
        scoped = dict(sm.scoped_state_data("s1"))
        assert scoped["count"] == 1
        assert scoped["label"] == "hi"
        assert scoped["items"] == [1, 2]
        assert scoped["local"] == {"a": 1}
        assert "calculated" not in scoped
        assert "sum" not in scoped

        sm.get_state_data("s1")["local"]["a"] = 9
        sm.send("go")
        assert sm.get_state_data("s1") is None
        assert sm.model.calculated == 3
        sm.send("back")
        assert sm.get_state_data("s1")["local"] == {"a": 1}

        other = machine_cls()
        assert dict(other.scoped_state_data("s1"))["items"] == [1, 2]


def test_scope_helpers_and_untyped_values():
    class NoCopy:
        def __deepcopy__(self, memo):
            raise RuntimeError("nope")

    class Fresh(StateChart):
        class parent(State.Compound):
            child = State(
                initial=True,
                final=True,
                data={"items": list, "plain": DataVar(default=1)},
            )

    DataVar(default=1).validate("n", 99)
    sm = Fresh()
    scoped = sm.scoped_state_data("child")
    assert len(scoped) == 2
    assert scoped["items"] == []
    sm.set_state_data("child", "plain", NoCopy())
    change = sm.get_data_changes()[-1]
    assert isinstance(change.new_value, NoCopy)
    assert change.old_value == 1


def test_active_state_without_data_rejects_assignment():
    class Bare(StateChart):
        a = State(initial=True, data={"n": 1})
        b = State()
        go = a.to(b)
        back = b.to(a)

    sm = Bare()
    sm.send("go")
    with pytest.raises(InvalidDefinition, match="not an active"):
        sm.set_state_data("b", "n", 1)
    assert sm.get_state_data("b") is None
