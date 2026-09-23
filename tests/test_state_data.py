"""State-scoped data: declaration, lifecycle, scoping, history and public API."""

import pickle
from functools import partial
from inspect import isawaitable

import pytest
from statemachine.exceptions import InvalidDefinition
from statemachine.exceptions import InvalidStateValue
from statemachine.state_data import StateDataView

from statemachine import DataChangeInfo
from statemachine import DataVar
from statemachine import HistoryState
from statemachine import State
from statemachine import StateChart
from statemachine import StateMachine


async def enabled_event_ids(sm):
    result = sm.enabled_events()
    if isawaitable(result):
        result = await result
    return [event.id for event in result]


class Counter(StateChart):
    idle = State(initial=True, data={"count": DataVar(0, type=int), "items": list})
    busy = State(data={"job": None})

    start = idle.to(busy)
    stop = busy.to(idle)
    bump = idle.to.itself(internal=True, on="do_bump")
    restart = idle.to.itself()

    def do_bump(self, state_data):
        state_data["count"] += 1
        state_data["items"].append(state_data["count"])


class Nested(StateChart):
    class outer(State.Compound, data={"level": "outer", "shared": 1}):
        class middle(State.Compound):
            inner = State(initial=True, data={"level": "inner", "own": True})
            other = State()

            move = inner.to(other)

    done = State(final=True)

    finish = outer.to(done)


class Regions(StateChart):
    class both(State.Parallel, data={"common": "parallel"}):
        class left(State.Compound, data={"side": "left"}):
            l1 = State(initial=True, data={"l": 1})
            l2 = State(final=True)

            go_left = l1.to(l2)

        class right(State.Compound, data={"side": "right"}):
            r1 = State(initial=True, data={"r": 1})
            r2 = State(final=True)

            go_right = r1.to(r2)


class TestDeclaration:
    @pytest.mark.parametrize("data", [[("x", 1)], "x", 1])
    def test_data_requires_a_dict(self, data):
        with pytest.raises(InvalidDefinition, match="must be a dict"):
            State(data=data)

    def test_data_keys_must_be_strings(self):
        with pytest.raises(InvalidDefinition, match="keys must be strings"):
            State(data={1: "x"})

    def test_values_are_normalized_to_data_vars(self):
        var = DataVar(2, type=int)
        state = State(data={"plain": 1, "factory": list, "var": var})

        assert state.data["plain"].default == 1
        assert state.data["factory"].factory is list
        assert state.data["var"] is var

    def test_no_data_declared(self):
        assert State().data == {}

    def test_datavar_rejects_default_and_factory(self):
        with pytest.raises(InvalidDefinition, match="either 'default' or 'factory'"):
            DataVar(None, factory=list)

    def test_datavar_factory_must_be_callable(self):
        with pytest.raises(InvalidDefinition, match="must be callable"):
            DataVar(factory=1)  # type: ignore[arg-type]

    def test_datavar_type_must_be_a_type(self):
        with pytest.raises(InvalidDefinition, match="must be a type"):
            DataVar(type="int")

    def test_datavar_default_must_match_type(self):
        with pytest.raises(InvalidDefinition, match="expects int"):
            DataVar("zero", type=int)

    def test_datavar_accepts_a_tuple_of_types(self):
        var = DataVar("a", type=(int, str))
        assert var.describe("x") == "x: int | str = 'a'"

    def test_datavar_without_default_starts_as_none(self):
        var = DataVar(type=int)
        assert var.default is None
        assert var.initial_value() is None

    @pytest.mark.parametrize(
        ("var", "expected"),
        [
            (DataVar(), "DataVar()"),
            (DataVar(0, type=int), "DataVar(0, type=int)"),
            (DataVar(factory=list, type=list), "DataVar(factory=list, type=list)"),
            (DataVar(factory=partial(dict, a=1)), "DataVar(factory=functools.partial("),
        ],
    )
    def test_datavar_repr(self, var, expected):
        assert repr(var).startswith(expected)

    @pytest.mark.parametrize(
        ("var", "expected"),
        [
            (DataVar(), "x = None"),
            (DataVar(0, type=int), "x: int = 0"),
            (DataVar(factory=list), "x = list()"),
        ],
    )
    def test_datavar_describe(self, var, expected):
        assert var.describe("x") == expected

    def test_compound_and_parallel_accept_data_keyword(self):
        assert Nested.outer.data["level"].default == "outer"
        assert Regions.both.parallel
        assert Regions.both.data["common"].default == "parallel"

    def test_declarations_are_not_mutated_by_instances(self):
        sm = Counter()
        sm.send("bump")

        assert Counter.idle.data["count"].default == 0


class TestLifecycle:
    async def test_entry_initializes_data_from_defaults(self, sm_runner):
        sm = await sm_runner.start(Counter)

        assert sm.get_state_data("idle") == {"count": 0, "items": []}
        assert sm.get_state_data("busy") is None

    async def test_exit_removes_data_and_entry_initializes_the_target(self, sm_runner):
        sm = await sm_runner.start(Counter)
        await sm_runner.send(sm, "start")

        assert sm.get_state_data("idle") is None
        assert sm.get_state_data("busy") == {"job": None}

    async def test_reentering_resets_data_to_defaults(self, sm_runner):
        sm = await sm_runner.start(Counter)
        await sm_runner.send(sm, "bump")
        await sm_runner.send(sm, "bump")
        assert sm.get_state_data("idle") == {"count": 2, "items": [1, 2]}

        await sm_runner.send(sm, "start")
        await sm_runner.send(sm, "stop")

        assert sm.get_state_data("idle") == {"count": 0, "items": []}

    async def test_external_self_transition_resets_data(self, sm_runner):
        sm = await sm_runner.start(Counter)
        await sm_runner.send(sm, "bump")

        await sm_runner.send(sm, "restart")

        assert sm.get_state_data("idle") == {"count": 0, "items": []}

    async def test_internal_self_transition_keeps_data(self, sm_runner):
        sm = await sm_runner.start(Counter)
        await sm_runner.send(sm, "bump")

        assert sm.get_state_data("idle") == {"count": 1, "items": [1]}

    async def test_data_is_stored_per_instance(self, sm_runner):
        first = await sm_runner.start(Counter)
        second = await sm_runner.start(Counter)

        await sm_runner.send(first, "bump")

        assert first.get_state_data("idle")["items"] == [1]
        assert second.get_state_data("idle")["items"] == []

    async def test_mutable_defaults_are_copied_on_every_entry(self, sm_runner):
        class Machine(StateChart):
            a = State(initial=True, data={"tags": ["x"], "meta": {"n": [1]}})
            b = State()

            go = a.to(b) | b.to(a)

        sm = await sm_runner.start(Machine)
        sm.get_state_data("a")["tags"].append("y")
        sm.get_state_data("a")["meta"]["n"].append(2)
        await sm_runner.send(sm, "go")
        await sm_runner.send(sm, "go")

        assert sm.get_state_data("a") == {"tags": ["x"], "meta": {"n": [1]}}

    async def test_factories_produce_a_fresh_value_per_entry(self, sm_runner):
        calls = []

        def make_token():
            calls.append(1)
            return len(calls)

        class Machine(StateChart):
            a = State(initial=True, data={"token": make_token, "var": DataVar(factory=dict)})
            b = State()

            go = a.to(b) | b.to(a)

        sm = await sm_runner.start(Machine)
        assert sm.get_state_data("a") == {"token": 1, "var": {}}

        await sm_runner.send(sm, "go")
        await sm_runner.send(sm, "go")

        assert sm.get_state_data("a") == {"token": 2, "var": {}}

    async def test_factory_result_must_match_type(self, sm_runner):
        class Machine(StateChart):
            a = State(initial=True, data={"n": DataVar(factory=str, type=int)})
            b = State(final=True)

            go = a.to(b)

        with pytest.raises(InvalidDefinition, match="factory result expects int"):
            await sm_runner.start(Machine)

    async def test_data_is_available_on_enter_and_exit(self, sm_runner):
        seen = []

        class Machine(StateChart):
            a = State(initial=True, data={"visits": 0})
            b = State(final=True)

            go = a.to(b)

            def on_enter_a(self, state_data):
                state_data["visits"] += 1

            def on_exit_a(self, state_data):
                seen.append(dict(state_data))

        sm = await sm_runner.start(Machine)
        assert sm.get_state_data("a") == {"visits": 1}

        await sm_runner.send(sm, "go")

        assert seen == [{"visits": 1}]
        assert sm.get_state_data("a") is None

    async def test_data_of_states_restored_from_the_model(self, sm_runner):
        class Model:
            state = "busy"

        sm = await sm_runner.start(Counter, model=Model())

        assert sm.get_state_data("busy") == {"job": None}
        assert sm.get_state_data("idle") is None


class TestScoping:
    async def test_child_sees_ancestor_data_and_shadows_on_collision(self, sm_runner):
        seen = {}

        class Machine(Nested):
            def on_enter_inner(self, state_data):
                seen.update(state_data)

        sm = await sm_runner.start(Machine)

        assert seen == {"level": "inner", "shared": 1, "own": True}
        assert sm.get_state_data("outer") == {"level": "outer", "shared": 1}

    async def test_assignment_is_routed_to_the_owner_state(self, sm_runner):
        class Machine(Nested):
            def on_enter_inner(self, state_data):
                state_data["shared"] = 2
                state_data["level"] = "changed"

        sm = await sm_runner.start(Machine)

        assert sm.get_state_data("outer") == {"level": "outer", "shared": 2}
        assert sm.get_state_data("inner") == {"level": "changed", "own": True}

    async def test_ancestor_data_is_visible_after_moving_between_children(self, sm_runner):
        seen = {}

        class Machine(Nested):
            def on_enter_other(self, state_data):
                seen.update(state_data)

        sm = await sm_runner.start(Machine)
        await sm_runner.send(sm, "move")

        assert seen == {"level": "outer", "shared": 1}

    async def test_parallel_regions_are_isolated(self, sm_runner):
        seen = {}

        class Machine(Regions):
            def on_enter_l1(self, state_data):
                seen["l1"] = dict(state_data)

            def on_enter_r1(self, state_data):
                seen["r1"] = dict(state_data)

        await sm_runner.start(Machine)

        assert seen == {
            "l1": {"common": "parallel", "side": "left", "l": 1},
            "r1": {"common": "parallel", "side": "right", "r": 1},
        }

    async def test_transition_callbacks_see_the_scope_of_their_state(self, sm_runner):
        seen = {}

        class Machine(StateChart):
            class parent(State.Compound, data={"p": 1}):
                a = State(initial=True, data={"a": 1})
                b = State(data={"b": 1})

                go = a.to(b, cond="check", before="log_before", on="log_on", after="log_after")

            def check(self, state_data):
                seen["cond"] = dict(state_data)
                return True

            def log_before(self, state_data):
                seen["before"] = dict(state_data)

            def log_on(self, state_data):
                seen["on"] = dict(state_data)

            def log_after(self, state_data):
                seen["after"] = dict(state_data)

        sm = await sm_runner.start(Machine)
        await sm_runner.send(sm, "go")

        assert seen == {
            "cond": {"p": 1, "a": 1},
            "before": {"p": 1, "a": 1},
            "on": {"p": 1},
            "after": {"p": 1, "b": 1},
        }

    async def test_each_exited_state_sees_its_own_data(self, sm_runner):
        seen = {}

        class Machine(StateChart):
            class both(State.Parallel, data={"common": "parallel"}):
                class left(State.Compound, data={"side": "left"}):
                    l1 = State(initial=True, data={"l": 1})

                class right(State.Compound, data={"side": "right"}):
                    r1 = State(initial=True, data={"r": 1})

            end = State(final=True)

            leave = both.to(end)

            def on_exit_r1(self, state_data):
                seen["r1"] = dict(state_data)

            def on_exit_left(self, state_data):
                seen["left"] = dict(state_data)

        sm = await sm_runner.start(Machine)
        await sm_runner.send(sm, "leave")

        assert seen == {
            "r1": {"common": "parallel", "side": "right", "r": 1},
            "left": {"common": "parallel", "side": "left"},
        }

    async def test_machines_without_data_inject_an_empty_view(self, sm_runner):
        seen = []

        class Machine(StateChart):
            a = State(initial=True)
            b = State(final=True)

            go = a.to(b)

            def on_exit_a(self, state_data):
                seen.append(dict(state_data))

            def on_enter_b(self, state_data):
                seen.append(len(state_data))
                with pytest.raises(InvalidDefinition, match="no active state data variable"):
                    state_data["x"] = 1

        sm = await sm_runner.start(Machine)
        await sm_runner.send(sm, "go")

        assert seen == [{}, 0]

    async def test_enabled_events_inject_state_data_on_guards(self, sm_runner):
        class Machine(StateChart):
            a = State(initial=True, data={"ready": False})
            b = State(final=True)

            go = a.to(b, cond="is_ready")

            def is_ready(self, state_data):
                return state_data["ready"]

        sm = await sm_runner.start(Machine)
        assert await enabled_event_ids(sm) == []

        sm.set_state_data("a", "ready", True)

        assert await enabled_event_ids(sm) == ["go"]


class TestStateDataView:
    @pytest.fixture()
    def view(self):
        sm = Nested()
        return sm, sm._state_data.view(sm.inner)

    def test_is_a_merged_mapping(self, view):
        _sm, data = view

        assert isinstance(data, StateDataView)
        assert data == {"level": "inner", "shared": 1, "own": True}
        assert len(data) == 3
        assert sorted(data) == ["level", "own", "shared"]
        assert "shared" in data
        assert data.get("missing", "default") == "default"
        assert repr(data) == "StateDataView({'level': 'inner', 'shared': 1, 'own': True})"

    def test_missing_key_raises_key_error(self, view):
        _sm, data = view

        with pytest.raises(KeyError):
            data["missing"]

    def test_assigning_an_undeclared_key_raises(self, view):
        _sm, data = view

        with pytest.raises(InvalidDefinition, match="no active state data variable"):
            data["missing"] = 1

    def test_keys_cannot_be_deleted(self, view):
        _sm, data = view

        with pytest.raises(InvalidDefinition, match="cannot be deleted"):
            del data["own"]

    def test_is_live(self, view):
        sm, data = view

        sm.set_state_data("outer", "shared", 5)
        assert data["shared"] == 5

        sm.send("finish")
        assert data == {}


class TestPublicAPI:
    def test_get_state_data_accepts_states_ids_and_values(self):
        class Machine(StateChart):
            a = State(initial=True, value=10, data={"x": 1})
            b = State(final=True)

            go = a.to(b)

        sm = Machine()

        assert sm.get_state_data(sm.a) == {"x": 1}
        assert sm.get_state_data(Machine.a) == {"x": 1}
        assert sm.get_state_data("a") == {"x": 1}
        assert sm.get_state_data(10) == {"x": 1}

    def test_get_state_data_of_unknown_state_raises(self):
        sm = Counter()

        with pytest.raises(InvalidStateValue):
            sm.get_state_data("unknown")

    def test_get_state_data_of_active_state_without_data(self):
        sm = Nested()

        assert "middle" in sm.configuration_values
        assert sm.get_state_data("middle") is None

    def test_get_state_data_returns_the_live_dict(self):
        sm = Counter()

        sm.get_state_data("idle")["items"].append("x")

        assert sm.get_state_data("idle")["items"] == ["x"]

    def test_state_data_values_is_a_snapshot(self):
        sm = Nested()
        snapshot = sm.state_data_values

        sm.set_state_data("outer", "shared", 2)

        assert snapshot == {
            "outer": {"level": "outer", "shared": 1},
            "inner": {"level": "inner", "own": True},
        }
        assert sm.state_data_values["outer"]["shared"] == 2

    def test_set_state_data(self):
        sm = Counter()

        sm.set_state_data(sm.idle, "count", 3)

        assert sm.get_state_data("idle")["count"] == 3

    def test_set_state_data_requires_an_active_state(self):
        sm = Counter()

        with pytest.raises(InvalidDefinition, match="inactive state 'busy'"):
            sm.set_state_data("busy", "job", "x")

    def test_set_state_data_requires_a_declared_key(self):
        sm = Counter()

        with pytest.raises(InvalidDefinition, match="does not declare the data variable"):
            sm.set_state_data("idle", "missing", 1)

    def test_set_state_data_enforces_the_type(self):
        sm = Counter()

        with pytest.raises(InvalidDefinition, match="expects int, got 'three'"):
            sm.set_state_data("idle", "count", "three")

        assert sm.get_state_data("idle")["count"] == 0

    def test_type_violation_in_callbacks_is_not_an_error_event(self):
        class Machine(StateChart):
            a = State(initial=True, data={"n": DataVar(0, type=int)})
            b = State(final=True)

            go = a.to(b, before="assign")
            error_execution = a.to(b)

            def assign(self, state_data):
                state_data["n"] = "x"

        sm = Machine()

        with pytest.raises(InvalidDefinition, match="expects int"):
            sm.send("go")
        assert sm.get_state_data("a") == {"n": 0}


class TestDataChanges:
    async def test_changes_are_recorded_during_the_macrostep(self, sm_runner):
        sm = await sm_runner.start(Counter)

        await sm_runner.send(sm, "bump")

        assert sm.get_data_changes() == [DataChangeInfo("idle", "count", 0, 1)]
        change = sm.get_data_changes()[0]
        assert (change.state_id, change.key, change.old_value, change.new_value) == (
            "idle",
            "count",
            0,
            1,
        )

    async def test_changes_are_cleared_at_each_macrostep(self, sm_runner):
        sm = await sm_runner.start(Counter)
        await sm_runner.send(sm, "bump")

        await sm_runner.send(sm, "start")

        assert sm.get_data_changes() == []

    async def test_changes_accumulate_until_the_next_macrostep(self, sm_runner):
        sm = await sm_runner.start(Counter)
        await sm_runner.send(sm, "bump")

        sm.set_state_data("idle", "count", 10)

        assert sm.get_data_changes() == [
            DataChangeInfo("idle", "count", 0, 1),
            DataChangeInfo("idle", "count", 1, 10),
        ]

    def test_changes_list_is_a_copy(self):
        sm = Counter()
        sm.set_state_data("idle", "count", 1)

        sm.get_data_changes().clear()

        assert len(sm.get_data_changes()) == 1


class HistoryMachine(StateChart):
    class area(State.Compound, data={"area_visits": 0}):
        class room(State.Compound, data={"room_n": 0}):
            desk = State(initial=True, data={"desk_n": 0})

            tick = desk.to.itself(internal=True, on="inc")

        deep = HistoryState(type="deep")
        shallow = HistoryState()

    outside = State()

    leave = area.to(outside)
    back_deep = outside.to(area.deep)
    back_shallow = outside.to(area.shallow)

    def inc(self, state_data):
        state_data["desk_n"] += 1
        state_data["room_n"] += 1
        state_data["area_visits"] += 1


class TestHistory:
    async def test_deep_history_restores_the_data_of_all_descendants(self, sm_runner):
        sm = await sm_runner.start(HistoryMachine)
        await sm_runner.send(sm, "tick")
        await sm_runner.send(sm, "leave")
        assert sm.state_data_values == {}

        await sm_runner.send(sm, "back_deep")

        assert sm.state_data_values == {
            "area": {"area_visits": 0},
            "room": {"room_n": 1},
            "desk": {"desk_n": 1},
        }

    async def test_shallow_history_restores_the_data_of_direct_children(self, sm_runner):
        sm = await sm_runner.start(HistoryMachine)
        await sm_runner.send(sm, "tick")
        await sm_runner.send(sm, "leave")

        await sm_runner.send(sm, "back_shallow")

        assert sm.state_data_values == {
            "area": {"area_visits": 0},
            "room": {"room_n": 1},
            "desk": {"desk_n": 0},
        }

    async def test_history_without_record_uses_defaults(self, sm_runner):
        class Machine(StateChart):
            outside = State(initial=True)

            class area(State.Compound):
                hall = State(initial=True)
                room = State(data={"n": 0})
                h = HistoryState()

                _ = h.to(room)

            enter = outside.to(area.h)

        sm = await sm_runner.start(Machine)
        await sm_runner.send(sm, "enter")

        assert sm.get_state_data("room") == {"n": 0}

    async def test_history_of_states_without_data(self, sm_runner):
        class Machine(StateChart):
            class area(State.Compound, data={"n": 0}):
                hall = State(initial=True)
                h = HistoryState(type="deep")

            outside = State()

            leave = area.to(outside)
            back = outside.to(area.h)

        sm = await sm_runner.start(Machine)
        await sm_runner.send(sm, "leave")
        await sm_runner.send(sm, "back")

        assert "hall" in sm.configuration_values
        assert sm.state_data_values == {"area": {"n": 0}}

    async def test_history_records_changes_made_on_exit(self, sm_runner):
        class Machine(HistoryMachine):
            def on_exit_desk(self, state_data):
                state_data["desk_n"] = 99

        sm = await sm_runner.start(Machine)
        await sm_runner.send(sm, "leave")
        await sm_runner.send(sm, "back_deep")

        assert sm.get_state_data("desk") == {"desk_n": 99}

    async def test_snapshot_is_updated_on_every_exit(self, sm_runner):
        sm = await sm_runner.start(HistoryMachine)
        for _ in range(2):
            await sm_runner.send(sm, "tick")
            await sm_runner.send(sm, "leave")
            await sm_runner.send(sm, "back_deep")

        assert sm.state_data_values == {
            "area": {"area_visits": 0},
            "room": {"room_n": 2},
            "desk": {"desk_n": 2},
        }


class TestErrorRollback:
    def test_failed_microstep_restores_the_data(self):
        class Machine(StateMachine):
            a = State(initial=True, data={"n": 1})
            b = State(data={"m": 1})

            go = a.to(b)
            back = b.to(a)

            def on_enter_b(self):
                raise ValueError("boom")

        sm = Machine()

        with pytest.raises(ValueError, match="boom"):
            sm.send("go")

        assert list(sm.configuration_values) == ["a"]
        assert sm.state_data_values == {"a": {"n": 1}}

    async def test_failed_microstep_restores_the_data_on_error_events(self, sm_runner):
        class Machine(StateChart):
            a = State(initial=True, data={"n": 1})
            b = State(data={"m": 1})

            go = a.to(b, before="fail")
            error_execution = a.to(b, on="fail_again")
            back = b.to(a)

            def fail(self):
                raise ValueError("boom")

            def fail_again(self):
                raise ValueError("again")

        sm = await sm_runner.start(Machine)
        await sm_runner.send(sm, "go")

        assert list(sm.configuration_values) == ["a"]
        assert sm.state_data_values == {"a": {"n": 1}}


class NoData(StateChart):
    a = State(initial=True)
    b = State(final=True)

    go = a.to(b)


class DiagramMachine(StateChart):
    class tasks(State.Compound, data={"done": 0}):
        todo = State(initial=True, data={"count": DataVar(0, type=int), "items": list})
        doing = State(data={"owner": None}, enter="notify")

        work = todo.to(doing)

    class split(State.Parallel, data={"flag": True}):
        class left(State.Compound):
            l1 = State(initial=True)

        class right(State.Compound):
            r1 = State(initial=True)

    finish = tasks.to(split)

    def notify(self): ...


class TestDiagrams:
    def test_extract_describes_the_data_declarations(self):
        from statemachine.contrib.diagram.extract import extract

        graph = extract(DiagramMachine)
        tasks, split = graph.states
        todo, doing = tasks.children

        assert tasks.data == ["done = 0"]
        assert todo.data == ["count: int = 0", "items = list()"]
        assert doing.data == ["owner = None"]
        assert split.data == ["flag = True"]
        assert split.children[0].data == []

    def test_dot_annotates_atomic_and_compound_states(self):
        from statemachine.contrib.diagram import DotGraphMachine

        dot = DotGraphMachine(DiagramMachine)().to_string()

        assert (
            '<tr><td align="left" cellpadding="6">'
            '<font point-size="9">count: int = 0</font><br/>'
            '<font point-size="9">items = list()</font></td></tr></table>'
        ) in dot
        assert (
            '<font point-size="9">owner = None</font></td></tr><hr/>'
            '<tr><td align="left" cellpadding="6">'
            '<font point-size="9">entry / notify</font></td></tr>'
        ) in dot
        assert '<b>Tasks</b><br/><font point-size="9">done = 0</font>' in dot
        assert '<b>Split</b> &#9783;<br/><font point-size="9">flag = True</font>' in dot

    def test_mermaid_annotates_atomic_states(self):
        mermaid = format(DiagramMachine, "mermaid")

        assert "todo : count: int = 0\n" in mermaid
        assert "todo : items = list()\n" in mermaid
        assert "doing : owner = None\n        doing : entry / notify\n" in mermaid


class TestSerialization:
    def test_machines_without_data_survive_pickle(self):
        sm = pickle.loads(pickle.dumps(NoData()))

        assert sm.state_data_values == {}
        assert sm.get_state_data("a") is None

    async def test_data_survives_pickle(self, sm_runner):
        sm = await sm_runner.start(Nested)
        sm.set_state_data("outer", "shared", 42)

        restored = pickle.loads(pickle.dumps(sm))

        assert restored.state_data_values == {
            "outer": {"level": "outer", "shared": 42},
            "inner": {"level": "inner", "own": True},
        }
        restored.set_state_data("inner", "own", False)
        assert sm.get_state_data("inner") == {"level": "inner", "own": True}
