import pickle
from functools import partial
from typing import Any
from xml.etree.ElementTree import Element

import pytest
from statemachine.contrib.diagram.extract import extract
from statemachine.contrib.diagram.renderers.mermaid import MermaidRenderer
from statemachine.exceptions import InvalidDefinition
from statemachine.io.scxml.parser import parse_direct_data_literals
from statemachine.io.scxml.processor import SCXMLProcessor
from statemachine.io.scxml.schema import State as ScxmlState
from statemachine.io.scxml.schema import StateMachineDefinition
from statemachine.state_data import StateData
from statemachine.state_data import normalize_data

from statemachine import DataChangeInfo
from statemachine import DataVar
from statemachine import HistoryState
from statemachine import State
from statemachine import StateChart


class _Named:
    def __call__(self) -> list:
        return [1]


class TestDataVar:
    def test_rejects_default_and_factory(self):
        with pytest.raises(InvalidDefinition, match="both"):
            DataVar(default=1, factory=list)

    def test_plain_value_and_factory_and_type(self):
        plain = DataVar(default={"n": 1})
        assert plain.produce() == {"n": 1}
        assert plain.produce() is not plain.default
        assert plain.default == {"n": 1}

        made = DataVar(factory=list)
        assert made.default is None
        assert made.produce() == []
        assert made.produce() is not made.produce()

        typed = DataVar(default=1, type=int)
        typed.check(2)
        with pytest.raises(InvalidDefinition, match="does not match"):
            typed.check("no")

        untyped = DataVar()
        assert untyped.produce() is None
        with pytest.raises(InvalidDefinition, match="does not match"):
            DataVar(factory=lambda: "x", type=int).produce()

    def test_annotations(self):
        assert DataVar(default=1).annotation("count") == "count = 1"
        assert DataVar(factory=list).annotation("items") == "items = list()"
        assert DataVar(default=1, type=int).annotation("count") == "count: int = 1"
        assert DataVar(type=(int, str)).annotation("value") == "value: int | str"
        assert DataVar(factory=_Named()).annotation("items") == "items = factory()"
        assert DataVar(factory=partial(list)).annotation("items") == "items = factory()"
        assert DataVar().annotation("flag") == "flag"


class TestNormalizeData:
    def test_requires_a_mapping_of_strings(self):
        assert normalize_data(None) == {}
        with pytest.raises(InvalidDefinition, match="must be a dict"):
            normalize_data(["nope"])  # type: ignore[arg-type]
        with pytest.raises(InvalidDefinition, match="keys must be strings"):
            normalize_data({1: "x"})  # type: ignore[dict-item]

    def test_wraps_values_and_callables(self):
        spec = normalize_data({"count": 1, "items": list, "typed": DataVar(default=0, type=int)})
        assert spec["count"].produce() == 1
        assert spec["items"].produce() == []
        assert spec["typed"].type is int


class TestStateDataStorage:
    def test_lifecycle_restore_and_change_log(self):
        root = State()
        root._set_id("root")
        parent = State(data={"title": "home"})
        parent._set_id("parent")
        parent.parent = root
        child = State(data={"count": 0, "items": list})
        child._set_id("child")
        child.parent = parent
        bare = State()
        bare._set_id("bare")
        typed = State(data={"n": DataVar(default=1, type=int)})
        typed._set_id("typed")

        storage = StateData()
        storage.activate(bare)
        assert storage.values == {}

        storage.activate(parent)
        storage.activate(child)
        assert storage.scoped_values(child) == {"title": "home", "count": 0, "items": []}

        view = storage.scoped(child)
        view["title"] = "away"
        view["count"] = 3
        assert storage.values["parent"]["title"] == "away"
        assert storage.values["child"]["count"] == 3
        assert view["count"] == 3

        with pytest.raises(InvalidDefinition, match="no key"):
            view["missing"] = 1

        storage.begin_macrostep()
        assert storage.change_log() == []

        storage.pending_restore["child"] = {"count": 9}
        storage.activate(child)
        assert storage.values["child"] == {"count": 9, "items": []}

        storage.pending_restore["typed"] = {"n": "bad"}
        with pytest.raises(InvalidDefinition, match="does not match"):
            storage.activate(typed)

        with pytest.raises(InvalidDefinition, match="no data key"):
            storage.set_value(typed, "missing", 1)
        with pytest.raises(InvalidDefinition, match="not active"):
            storage.set_value(typed, "n", 2)

        storage.history["h"] = {}
        storage.saving_history_ids = ["missing", "h"]
        storage.deactivate(child)
        assert "child" not in storage.values
        assert storage.history["h"] == {}

        storage.activate(child)
        storage.history["h"] = {"child": {"count": 1, "items": []}}
        storage.saving_history_ids = ["h"]
        storage.values["child"]["count"] = 4
        storage.deactivate(child)
        assert storage.history["h"]["child"]["count"] == 4

        storage.save_history("empty", [bare])
        assert storage.history["empty"] == {}

        storage.queue_restore("absent", [child])
        storage.queue_restore("h", [child, bare])
        assert storage.pending_restore["child"]["count"] == 4

        assert storage.snapshot()["parent"]["title"] == "away"


class Counter(StateChart):
    draft = State(
        initial=True,
        data={"count": 0, "tag": DataVar(default="draft", type=str), "items": list},
        enter="on_enter_draft",
        exit="on_exit_draft",
    )
    published = State(data={"views": DataVar(factory=lambda: {"n": 0}, type=dict)})

    publish = draft.to(published, before="on_publish")
    revise = published.to(draft)
    bump = draft.to.itself(internal=True, after="on_bump")
    cycle = draft.to.itself()

    def __init__(self, **kwargs):
        self.seen: list[Any] = []
        super().__init__(**kwargs)

    def on_enter_draft(self, state_data):
        self.seen.append(("enter", dict(state_data)))

    def on_exit_draft(self, state_data):
        state_data["count"] += 1
        self.seen.append(("exit", state_data["count"]))

    def on_publish(self, source, target, event_data, state_data):
        self.seen.append((source.id, target.id, event_data.event, state_data["count"]))

    def on_bump(self, state_data):
        state_data["count"] += 1


class Hierarchy(StateChart):
    class outer(State.Compound, data={"title": "home", "count": 0}):
        inner = State(initial=True, data={"count": 1, "note": "child"}, enter="on_enter_inner")
        move = inner.to.itself(internal=True, after="on_move")

    away = State()
    leave = outer.to(away)
    back = away.to(outer)

    def on_enter_inner(self, state_data):
        self.entered_title = state_data["title"]

    def on_move(self, state_data):
        state_data["note"] = "moved"
        state_data["title"] = "away"


class Regions(StateChart):
    class outer(State.Parallel, data={"mode": "p"}):
        class left(State.Compound, data={"count": 1}):
            a = State(initial=True, data={"count": 10})
            go = a.to.itself(internal=True, cond="count_is_local", on="on_go")

        class right(State.Compound, data={"count": 2, "secret": "no"}):
            b = State(initial=True)
            wait = b.to.itself()

    done = State(final=True)
    finish = outer.to(done)

    def __init__(self, **kwargs):
        self.seen: dict[str, Any] = {}
        self.went = False
        super().__init__(**kwargs)

    def count_is_local(self, state_data):
        self.seen = dict(state_data)
        return (
            state_data.get("count") == 10
            and "secret" not in state_data
            and state_data.get("mode") == "p"
        )

    def on_go(self):
        self.went = True


class DeepHistory(StateChart):
    class outer(State.Compound, data={"title": "root"}):
        h = HistoryState(type="deep")

        class inner(State.Compound, data={"count": 0}):
            a = State(initial=True, data={"items": list})
            b = State(data={"items": list})
            swap = a.to(b) | b.to(a)

    away = State(initial=True)
    dive = away.to(outer)
    leave = outer.to(away)
    restore = away.to(outer.h)  # type: ignore[attr-defined]


class ExitMutates(StateChart):
    class outer(State.Compound):
        h = HistoryState(type="deep")
        inner = State(initial=True, data={"n": 1}, exit="on_exit_inner")

    away = State(initial=True)
    dive = away.to(outer)
    leave = outer.to(away)
    restore = away.to(outer.h)  # type: ignore[attr-defined]

    def on_exit_inner(self, state_data):
        state_data["n"] = 7


class ShallowHistory(StateChart):
    class outer(State.Compound):
        h = HistoryState()

        class inner(State.Compound, data={"count": 0}):
            a = State(initial=True, data={"items": list})
            b = State()
            swap = a.to(b)

    away = State(initial=True)
    dive = away.to(outer)
    leave = outer.to(away)
    restore = away.to(outer.h)  # type: ignore[attr-defined]


class DiagramData(StateChart):
    validate_disconnected_states = False
    validate_trap_states = False

    class group(State.Compound, data={"title": "home"}, enter="on_enter_group"):
        ready = State(
            initial=True,
            data={"count": DataVar(default=0, type=int)},
            enter="on_enter_ready",
        )
        plain = State(data={"flag": True})
        go = ready.to(plain)

    class zones(State.Parallel, data={"mode": "p"}):
        class left(State.Compound):
            a = State(initial=True)

        class right(State.Compound):
            b = State(initial=True)

    def on_enter_group(self):
        return None

    def on_enter_ready(self):
        return None


class TestStateDataBehavior:
    async def test_entry_exit_reset_and_public_api(self, sm_runner):
        sm = await sm_runner.start(Counter)
        assert sm.scoped_state_data(None) == {}
        assert sm.get_state_data(sm.draft)["count"] == 0
        assert sm.get_state_data(sm.states_map["draft"])["tag"] == "draft"
        assert sm.get_state_data("published") is None
        assert sm.seen[-1] == ("enter", {"count": 0, "tag": "draft", "items": []})

        held = sm.get_state_data("draft")["items"]
        held.append("a")
        snap = sm.state_data_values
        snap["draft"]["items"].append("b")
        snap["draft"]["count"] = 99
        assert sm.get_state_data("draft")["items"] == ["a"]
        assert sm.get_state_data("draft")["count"] == 0

        await sm_runner.send(sm, "bump")
        assert sm.get_state_data(sm.draft)["count"] == 1
        assert sm.get_data_changes()[-1] == DataChangeInfo("draft", "count", 0, 1)

        await sm_runner.send(sm, "cycle")
        assert sm.get_state_data("draft") == {"count": 0, "tag": "draft", "items": []}
        assert sm.get_state_data("draft")["items"] is not held
        assert ("exit", 2) in sm.seen
        assert sm.seen[-1][0] == "enter"

        other = Counter()
        assert other.get_state_data("draft")["count"] == 0
        assert other.get_state_data("draft")["items"] is not sm.get_state_data("draft")["items"]

        await sm_runner.send(sm, "publish")
        assert ("draft", "published", "publish", 0) in sm.seen
        assert sm.get_state_data("draft") is None
        first_views = sm.get_state_data("published")["views"]
        assert first_views == {"n": 0}
        assert sm.state_data_values == {"published": {"views": {"n": 0}}}

        await sm_runner.send(sm, "revise")
        assert sm.get_state_data("draft") == {"count": 0, "tag": "draft", "items": []}
        assert sm.get_data_changes() == []

        await sm_runner.send(sm, "publish")
        assert sm.get_state_data("published")["views"] is not first_views

        with pytest.raises(InvalidDefinition, match="not active"):
            sm.set_state_data("draft", "count", 1)
        with pytest.raises(InvalidDefinition, match="no data key"):
            sm.set_state_data("published", "missing", 1)
        with pytest.raises(InvalidDefinition, match="does not match"):
            sm.set_state_data("published", "views", "nope")
        with pytest.raises(InvalidDefinition, match="Unknown state"):
            sm.get_state_data("missing")
        with pytest.raises(InvalidDefinition, match="Expected a state"):
            sm.get_state_data(1)  # type: ignore[arg-type]

        restored = pickle.loads(pickle.dumps(sm))
        assert restored.get_state_data("published")["views"] == {"n": 0}
        assert restored.published.is_active

    async def test_hierarchy_and_parallel_scopes(self, sm_runner):
        sm = await sm_runner.start(Hierarchy)
        assert sm.entered_title == "home"
        assert sm.get_state_data("outer")["count"] == 0
        await sm_runner.send(sm, "move")
        assert sm.get_state_data("outer")["title"] == "away"
        assert sm.get_state_data("outer")["count"] == 0
        assert sm.get_state_data("inner")["note"] == "moved"
        assert sm.get_state_data("inner")["count"] == 1
        assert {change.key for change in sm.get_data_changes()} == {"note", "title"}

        regions = await sm_runner.start(Regions)
        assert regions.get_state_data("left")["count"] == 1
        assert regions.get_state_data("a")["count"] == 10
        events: Any = regions.enabled_events()
        if hasattr(events, "__await__"):
            events = await events
        assert any(event.id == "go" for event in events)
        await sm_runner.send(regions, "go")
        assert regions.went is True
        assert regions.seen["count"] == 10
        assert "secret" not in regions.seen
        assert regions.seen["mode"] == "p"

    async def test_history_restores_snapshots(self, sm_runner):
        sm = await sm_runner.start(DeepHistory)
        await sm_runner.send(sm, "dive")
        sm.set_state_data("inner", "count", 4)
        await sm_runner.send(sm, "swap")
        sm.set_state_data("b", "items", ["x"])
        await sm_runner.send(sm, "leave")
        await sm_runner.send(sm, "restore")
        assert sm.get_state_data("outer")["title"] == "root"
        assert sm.get_state_data("inner")["count"] == 4
        assert sm.get_state_data("b")["items"] == ["x"]
        assert sm.get_state_data("a") is None

        mutated = await sm_runner.start(ExitMutates)
        await sm_runner.send(mutated, "dive")
        await sm_runner.send(mutated, "leave")
        await sm_runner.send(mutated, "restore")
        assert mutated.get_state_data("inner")["n"] == 7

        shallow = await sm_runner.start(ShallowHistory)
        await sm_runner.send(shallow, "dive")
        shallow.set_state_data("inner", "count", 2)
        shallow.set_state_data("a", "items", ["y"])
        await sm_runner.send(shallow, "swap")
        await sm_runner.send(shallow, "leave")
        await sm_runner.send(shallow, "restore")
        assert shallow.get_state_data("inner")["count"] == 2
        assert shallow.get_state_data("a")["items"] == []
        assert shallow.get_state_data("b") is None

    def test_invalid_declarations(self):
        with pytest.raises(InvalidDefinition, match="must be a dict"):

            class BadState(StateChart):
                start = State(initial=True, data=["nope"])  # type: ignore[arg-type]

        with pytest.raises(InvalidDefinition, match="both"):

            class BadVar(StateChart):
                start = State(initial=True, data={"n": DataVar(default=1, factory=list)})

        with pytest.raises(InvalidDefinition, match="must be a dict"):

            class BadCompound(StateChart):
                class outer(State.Compound, data="nope"):  # type: ignore[misc]
                    inner = State(initial=True)


class TestScxmlDataLiterals:
    def test_literals_are_state_data(self):
        scxml = """
        <scxml xmlns="http://www.w3.org/2005/07/scxml" initial="s1">
          <datamodel>
            <data id="mode" expr="'run'"/>
            <data id="count" expr="9"/>
            <data id="missing"/>
          </datamodel>
          <state id="s1">
            <datamodel>
              <data id="count" expr="1"/>
              <data id="label" expr="'ready'"/>
            </datamodel>
            <state id="inner">
              <datamodel>
                <data id="note" expr="'child'"/>
              </datamodel>
            </state>
          </state>
        </scxml>
        """
        processor = SCXMLProcessor()
        processor.parse_scxml("literals", scxml)
        sm = processor.start()
        assert sm.get_state_data("s1") == {"mode": "run", "count": 1, "label": "ready"}
        assert sm.get_state_data("inner") == {"note": "child"}

        element = Element("scxml")
        datamodel = Element("datamodel")
        datamodel.append(Element("data", {"id": "bad", "expr": "1+2"}))
        datamodel.append(Element("data", {"id": "broken", "expr": "["}))
        datamodel.append(Element("data", {"expr": "1"}))
        datamodel.append(Element("data", {"id": "ok", "expr": "1"}))
        element.append(datamodel)
        assert parse_direct_data_literals(element) == {"ok": 1}
        assert parse_direct_data_literals(Element("scxml")) == {}

    def test_root_data_without_explicit_initial(self):
        definition = StateMachineDefinition(
            name="manual",
            states={"s1": ScxmlState(id="s1")},
            root_data={"mode": "run"},
        )
        processor = SCXMLProcessor()
        processor.process_definition(definition, location="manual")
        sm = processor.start()
        assert sm.get_state_data("s1")["mode"] == "run"

        processor._apply_root_data({}, None)
        processor._apply_root_data({}, {"mode": "run"})


class TestDiagramDataLabels:
    def test_labels_include_data_variables(self):
        graph = extract(DiagramData)
        by_id = {}

        def walk(states):
            for state in states:
                by_id[state.id] = state
                walk(state.children)

        walk(graph.states)
        assert by_id["group"].data == ["title = 'home'"]
        assert by_id["ready"].data == ["count: int = 0"]
        assert by_id["plain"].data == ["flag = True"]
        assert by_id["zones"].data == ["mode = 'p'"]

        mermaid = MermaidRenderer().render(graph)
        assert "count: int = 0" in mermaid
        assert "title = 'home'" in mermaid
        assert "mode = 'p'" in mermaid

        from statemachine.contrib.diagram.renderers.dot import DotRenderer

        dot = DotRenderer().render(graph).to_string()
        assert "count: int = 0" in dot
        assert "title = 'home'" in dot
        assert "mode = 'p'" in dot
        assert "flag = True" in dot
