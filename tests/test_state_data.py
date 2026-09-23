import pickle

import pytest
from statemachine.contrib.diagram.extract import extract
from statemachine.contrib.diagram.model import ActionType
from statemachine.contrib.diagram.renderers.mermaid import MermaidRenderer
from statemachine.exceptions import InvalidDefinition
from statemachine.io import create_machine_class_from_definition
from statemachine.io.scxml.parser import parse_scxml
from statemachine.io.scxml.processor import SCXMLProcessor

from statemachine import DataChangeInfo
from statemachine import DataVar
from statemachine import HistoryState
from statemachine import State
from statemachine import StateChart
from statemachine import StateMachine


class TestDataVar:
    def test_default_is_deep_copied(self):
        var = DataVar([1, [2]])
        first = var.create()
        first[1].append(3)
        assert var.create() == [1, [2]]

    def test_factory_produces_fresh_values(self):
        var = DataVar(factory=list)
        assert var.create() is not var.create()

    def test_missing_default_creates_none(self):
        assert DataVar().create() is None

    def test_default_and_factory_are_mutually_exclusive(self):
        with pytest.raises(InvalidDefinition, match="both"):
            DataVar(0, factory=int)

    def test_factory_must_be_callable(self):
        with pytest.raises(InvalidDefinition, match="callable"):
            DataVar(factory=1)  # type: ignore[arg-type]

    def test_default_must_match_type(self):
        with pytest.raises(InvalidDefinition, match="expected int"):
            DataVar("x", type=int)

    def test_factory_result_must_match_type(self):
        with pytest.raises(InvalidDefinition, match="expected int"):
            DataVar(factory=str, type=int).create()

    def test_tuple_of_types(self):
        var = DataVar(1, type=(int, float))
        var.validate("x", 1.5)
        with pytest.raises(InvalidDefinition, match="int | float"):
            var.validate("x", "a")

    def test_repr(self):
        assert repr(DataVar(0, type=int)) == "DataVar(default=0, type=int)"
        assert repr(DataVar(factory=list)) == "DataVar(factory=list)"
        assert repr(DataVar()) == "DataVar()"

    def test_describe(self):
        assert DataVar(0, type=int).describe("n") == "n: int = 0"
        assert DataVar(factory=list).describe("items") == "items = list()"
        assert DataVar(type=str).describe("name") == "name: str"

    def test_equality(self):
        assert DataVar(0, type=int) == DataVar(0, type=int)
        assert DataVar(0) != DataVar(1)
        assert DataVar(0) != 0


class TestDeclaration:
    def test_data_must_be_a_dict(self):
        with pytest.raises(InvalidDefinition, match="must be a dict"):
            State(data=[("a", 1)])  # type: ignore[arg-type]

    def test_data_keys_must_be_strings(self):
        with pytest.raises(InvalidDefinition, match="keys must be strings"):
            State(data={1: "a"})  # type: ignore[dict-item]

    def test_values_are_normalized(self):
        state = State(data={"a": 1, "b": list, "c": DataVar(0, type=int)})
        assert state.data_spec == {
            "a": DataVar(1),
            "b": DataVar(factory=list),
            "c": DataVar(0, type=int),
        }

    def test_no_data(self):
        assert State().data_spec is None

    def test_compound_and_parallel_keyword(self):
        class SC(StateChart):
            class comp(State.Compound, data={"x": 1}):
                a = State(initial=True)

            class par(State.Parallel, data={"y": 2}):
                class r1(State.Compound):
                    b = State(initial=True)

            go = comp.to(par)

        assert SC.comp.data_spec == {"x": DataVar(1)}
        assert SC.par.data_spec == {"y": DataVar(2)}

    def test_dict_definition(self):
        cls = create_machine_class_from_definition(
            "Defined",
            states={
                "a": {"initial": True, "data": {"n": 1}, "on": {"go": [{"target": "b"}]}},
                "b": {"final": True},
            },
        )
        sm = cls()
        assert sm.state_data_values == {"a": {"n": 1}}


class Lifecycle(StateChart):
    a = State(initial=True, data={"items": [], "made": list, "n": DataVar(0, type=int)})
    b = State()

    go = a.to(b)
    back = b.to(a)
    loop = a.to.itself()


class TestLifecycle:
    async def test_initialized_on_entry(self, sm_runner):
        sm = await sm_runner.start(Lifecycle)
        assert sm.get_state_data("a") == {"items": [], "made": [], "n": 0}

    async def test_removed_on_exit_and_reset_on_reentry(self, sm_runner):
        sm = await sm_runner.start(Lifecycle)
        sm.get_state_data(sm.a)["items"].append(1)
        sm.set_state_data("a", "n", 5)

        await sm_runner.send(sm, "go")
        assert sm.get_state_data("a") is None
        assert sm.state_data_values == {}

        await sm_runner.send(sm, "back")
        assert sm.get_state_data("a") == {"items": [], "made": [], "n": 0}

    async def test_self_transition_resets(self, sm_runner):
        sm = await sm_runner.start(Lifecycle)
        sm.set_state_data("a", "n", 5)
        await sm_runner.send(sm, "loop")
        assert sm.get_state_data("a")["n"] == 0

    async def test_stored_per_instance(self, sm_runner):
        sm1 = await sm_runner.start(Lifecycle)
        sm2 = await sm_runner.start(Lifecycle)
        sm1.get_state_data("a")["items"].append(1)
        sm1.get_state_data("a")["made"].append(1)
        assert sm2.get_state_data("a") == {"items": [], "made": [], "n": 0}
        assert Lifecycle.a.data_spec["items"].default == []

    def test_internal_transition_keeps_data(self):
        class SC(StateChart):
            a = State(initial=True, data={"n": 0})
            poke = a.to.itself(internal=True)

        sm = SC()
        sm.set_state_data("a", "n", 1)
        sm.send("poke")
        assert sm.get_state_data("a") == {"n": 1}

    def test_survives_pickle(self):
        sm = Lifecycle()
        sm.set_state_data("a", "n", 7)
        restored = pickle.loads(pickle.dumps(sm))
        assert restored.state_data_values == {"a": {"items": [], "made": [], "n": 7}}
        restored.set_state_data("a", "n", 8)
        assert sm.get_state_data("a")["n"] == 7


class Scoped(StateChart):
    class outer(State.Compound, data={"shared": "outer", "name": "outer"}):
        inner = State(initial=True, data={"name": "inner", "local": 1})
        other = State()
        move = inner.to(other)

    class par(State.Parallel, data={"p": 0}):
        class left(State.Compound, data={"side": "left"}):
            l1 = State(initial=True, data={"lv": 1})

        class right(State.Compound, data={"side": "right"}):
            r1 = State(initial=True)

    jump = outer.to(par)


class TestScoping:
    async def test_child_shadows_parent(self, sm_runner):
        seen = {}

        class Listener:
            def on_enter_inner(self, state_data):
                seen["inner"] = dict(state_data)

            def on_enter_other(self, state_data):
                seen["other"] = dict(state_data)

        sm = await sm_runner.start(Scoped, listeners=[Listener()])
        assert seen["inner"] == {"shared": "outer", "name": "inner", "local": 1}
        await sm_runner.send(sm, "move")
        assert seen["other"] == {"shared": "outer", "name": "outer"}

    async def test_parallel_regions_are_isolated(self, sm_runner):
        seen = {}

        class Listener:
            def on_enter_l1(self, state_data):
                seen["l1"] = dict(state_data)

            def on_enter_r1(self, state_data):
                seen["r1"] = dict(state_data)

        sm = await sm_runner.start(Scoped, listeners=[Listener()])
        await sm_runner.send(sm, "jump")
        assert seen["l1"] == {"p": 0, "side": "left", "lv": 1}
        assert seen["r1"] == {"p": 0, "side": "right"}
        assert sm.state_data_values == {
            "par": {"p": 0},
            "left": {"side": "left"},
            "l1": {"lv": 1},
            "right": {"side": "right"},
        }

    async def test_writes_go_to_owning_scope(self, sm_runner):
        class Listener:
            def on_enter_inner(self, state_data):
                state_data["shared"] = "changed"
                state_data["name"] = "renamed"

        sm = await sm_runner.start(Scoped, listeners=[Listener()])
        assert sm.get_state_data("outer") == {"shared": "changed", "name": "outer"}
        assert sm.get_state_data("inner")["name"] == "renamed"

    def test_view_mapping_protocol(self):
        sm = Scoped()
        view = sm._state_data_store.scope(sm.inner)
        assert "shared" in view
        assert "missing" not in view
        assert len(view) == 3
        assert sorted(view) == ["local", "name", "shared"]
        assert view == {"shared": "outer", "name": "inner", "local": 1}
        assert repr(view) == repr({"shared": "outer", "name": "inner", "local": 1})
        with pytest.raises(KeyError):
            view["missing"]
        with pytest.raises(InvalidDefinition, match="declares a data key 'missing'"):
            view["missing"] = 1
        with pytest.raises(InvalidDefinition, match="cannot be deleted"):
            del view["name"]

    def test_view_without_state_is_empty(self):
        sm = Scoped()
        assert dict(sm._state_data_store.scope(None)) == {}


class Callbacks(StateChart):
    a = State(initial=True, data={"count": DataVar(0, type=int), "log": list})
    b = State(data={"b_value": "b"})

    go = a.to(b, cond="can_go", before="before_go", after="after_go")
    back = b.to(a)

    def can_go(self, state_data):
        return state_data["count"] > 0

    def before_go(self, state_data):
        state_data["log"].append("before")

    def on_exit_a(self, state_data, source, target):
        state_data["log"].append("exit")
        self.exit_log = list(state_data["log"])

    def on_enter_a(self, state_data):
        state_data["log"].append("enter")

    def after_go(self, state_data):
        self.after_data = dict(state_data)


class TestCallbacks:
    async def test_state_data_injected_and_persistent(self, sm_runner):
        sm = await sm_runner.start(Callbacks)
        assert sm.get_state_data("a")["log"] == ["enter"]

        await sm_runner.send(sm, "go")
        assert "a" in sm.configuration_values

        sm.set_state_data("a", "count", 1)
        await sm_runner.send(sm, "go")
        assert sm.exit_log == ["enter", "before", "exit"]
        assert sm.after_data == {"b_value": "b"}

    async def test_enabled_events_guard_receives_state_data(self, sm_runner):
        sm = await sm_runner.start(Callbacks)
        result = sm.enabled_events()
        if sm_runner.is_async:
            result = await result
        assert result == []
        sm.set_state_data("a", "count", 2)
        result = sm.enabled_events()
        if sm_runner.is_async:
            result = await result
        assert [e.id for e in result] == ["go"]


class WithHistory(StateChart):
    class outer(State.Compound, data={"o": 0}):
        class mid(State.Compound, initial=True, data={"m": 0}):
            leaf1 = State(initial=True, data={"l": 0})
            leaf2 = State(data={"l2": 0})
            step = leaf1.to(leaf2)

        shallow = HistoryState()
        deep = HistoryState(type="deep")

    off = State(initial=True)
    enter = off.to(outer)
    leave = outer.to(off)
    back_shallow = off.to(outer.shallow)
    back_deep = off.to(outer.deep)


async def _prepare_history(sm_runner):
    sm = await sm_runner.start(WithHistory)
    await sm_runner.send(sm, "enter")
    await sm_runner.send(sm, "step")
    sm.set_state_data("outer", "o", 1)
    sm.set_state_data("mid", "m", 2)
    sm.set_state_data("leaf2", "l2", 3)
    await sm_runner.send(sm, "leave")
    assert sm.state_data_values == {}
    return sm


class TestHistory:
    async def test_deep_history_restores_all_descendants(self, sm_runner):
        sm = await _prepare_history(sm_runner)
        await sm_runner.send(sm, "back_deep")
        assert sm.state_data_values == {"outer": {"o": 0}, "mid": {"m": 2}, "leaf2": {"l2": 3}}

    async def test_shallow_history_restores_direct_children(self, sm_runner):
        sm = await _prepare_history(sm_runner)
        await sm_runner.send(sm, "back_shallow")
        assert sm.state_data_values == {"outer": {"o": 0}, "mid": {"m": 2}, "leaf1": {"l": 0}}

    async def test_plain_entry_ignores_history(self, sm_runner):
        sm = await _prepare_history(sm_runner)
        await sm_runner.send(sm, "enter")
        assert sm.state_data_values == {"outer": {"o": 0}, "mid": {"m": 0}, "leaf1": {"l": 0}}

    async def test_history_without_data_keeps_no_snapshot(self, sm_runner):
        class SC(StateChart):
            class outer(State.Compound):
                a = State(initial=True)
                h = HistoryState()

            off = State()
            leave = outer.to(off)
            back = off.to(outer.h)

        sm = await sm_runner.start(SC)
        await sm_runner.send(sm, "leave")
        assert sm._state_data_store.history == {}
        await sm_runner.send(sm, "back")
        assert "a" in sm.configuration_values


class TestAPI:
    def test_get_state_data(self):
        sm = Scoped()
        assert sm.get_state_data(sm.inner) is sm.get_state_data("inner")
        assert sm.get_state_data("other") is None
        assert sm.get_state_data("r1") is None

    def test_unknown_state_id(self):
        sm = Scoped()
        with pytest.raises(InvalidDefinition, match="Unknown state 'nope'"):
            sm.get_state_data("nope")

    def test_set_state_data_validations(self):
        sm = Callbacks()
        with pytest.raises(InvalidDefinition, match="not active"):
            sm.set_state_data("b", "b_value", "x")
        with pytest.raises(InvalidDefinition, match="does not declare"):
            sm.set_state_data("a", "missing", 1)
        with pytest.raises(InvalidDefinition, match="expected int"):
            sm.set_state_data("a", "count", "x")
        assert sm.get_state_data("a")["count"] == 0

    def test_set_state_data_on_active_state_without_data(self):
        class SC(StateChart):
            a = State(initial=True)
            b = State(final=True)
            go = a.to(b)

        with pytest.raises(InvalidDefinition, match="does not declare"):
            SC().set_state_data("a", "x", 1)

    def test_set_state_data_on_declared_state_without_initialized_data(self):
        class SM(StateMachine):
            a = State(initial=True, data={"x": 0})
            b = State(final=True)
            go = a.to(b)

        sm = SM()
        sm._state_data_store.exit(sm.a)
        with pytest.raises(InvalidDefinition, match="not active"):
            sm._state_data_store.set(sm.a, "x", 1)

    async def test_data_changes_cleared_at_macrostep_boundary(self, sm_runner):
        class SC(StateChart):
            a = State(initial=True, data={"n": 0})
            b = State(data={"m": 0})
            c = State(final=True)
            go = a.to(b)
            finish = b.to(c)

            def on_exit_a(self, state_data):
                state_data["n"] = 1

            def on_enter_b(self):
                self.set_state_data("b", "m", 2)

        sm = await sm_runner.start(SC)
        assert sm.get_data_changes() == []
        sm.set_state_data("a", "n", 5)
        assert sm.get_data_changes() == [DataChangeInfo("a", "n", 0, 5)]

        await sm_runner.send(sm, "go")
        changes = sm.get_data_changes()
        assert changes == [DataChangeInfo("a", "n", 5, 1), DataChangeInfo("b", "m", 0, 2)]
        assert (changes[0].state_id, changes[0].key) == ("a", "n")
        assert (changes[0].old_value, changes[0].new_value) == (5, 1)

        await sm_runner.send(sm, "go")
        assert sm.get_data_changes() == []


class TestSCXML:
    SCXML = """
    <scxml xmlns="http://www.w3.org/2005/07/scxml" initial="s1">
        <state id="s1">
            <datamodel>
                <data id="count" expr="3"/>
                <data id="names" expr="['a', 'b']"/>
                <data id="dynamic" expr="_event.data"/>
                <data id="noexpr"/>
            </datamodel>
            <transition event="go" target="s2"/>
        </state>
        <final id="s2"/>
    </scxml>
    """

    def test_parse_state_data_literals(self):
        definition = parse_scxml(self.SCXML)
        assert definition.states["s1"].data == {"count": 3, "names": ["a", "b"]}
        assert definition.states["s2"].data == {}

    def test_processor_creates_state_data(self):
        processor = SCXMLProcessor()
        processor.parse_scxml("state_data", self.SCXML)
        sm = processor.start()
        assert sm.get_state_data("s1") == {"count": 3, "names": ["a", "b"]}


class TestDiagram:
    def test_extract_annotates_data(self):
        graph = extract(Callbacks)
        state_a = next(s for s in graph.states if s.id == "a")
        data_actions = [a.body for a in state_a.actions if a.type == ActionType.DATA]
        assert data_actions == ["count: int = 0", "log = list()"]

    def test_mermaid_renders_data(self):
        output = MermaidRenderer().render(extract(Callbacks))
        assert "a : data / count: int = 0" in output

    def test_dot_renders_data(self):
        pytest.importorskip("pydot")
        from statemachine.contrib.diagram import DotGraphMachine

        dot = DotGraphMachine(Scoped)().to_string()
        assert "data / local = 1" in dot
        assert "data / shared = &#x27;outer&#x27;" in dot or "shared = 'outer'" in dot
