import { describe, test } from "node:test";
import * as assert from "node:assert/strict";
import { object } from "./constructs.ts";
import { formatMessage, message } from "./message.ts";
import type { NonEmptyString } from "./nonempty.ts";
import { withDefault } from "./modifiers.ts";
import {
  buildOptionReferenceMap,
  extractDependsOn,
  isDependsOnSatisfied,
  normalizeDependsOn,
  type OptionDependencyContext,
} from "./option-dependency.ts";
import { parseSync, type Suggestion } from "./parser.ts";
import {
  conditionalOption,
  option,
  optionalWhen,
  requiredWhen,
} from "./primitives.ts";
import { choice, string, type ValueParser, type ValueParserResult } from "./valueparser.ts";

function booleanValueParser(): ValueParser<"sync", boolean> {
  return {
    $mode: "sync",
    metavar: "BOOL" as NonEmptyString,
    parse(input: string): ValueParserResult<boolean> {
      const lower = input.toLowerCase();
      if (lower === "true" || lower === "1" || lower === "yes") {
        return { success: true, value: true };
      }
      if (lower === "false" || lower === "0" || lower === "no") {
        return { success: true, value: false };
      }
      return {
        success: false,
        error: message`Expected boolean (true/false).`,
      };
    },
    format(value: boolean): string {
      return value ? "true" : "false";
    },
  };
}

function assertErrorIncludes(error: unknown, text: string): void {
  const formatted = formatMessage(error as Parameters<typeof formatMessage>[0]);
  assert.ok(formatted.includes(text), formatted);
}

describe("normalizeDependsOn()", () => {
  test("accepts string conditions", () => {
    assert.deepEqual(normalizeDependsOn("mode"), { option: "mode" });
  });

  test("accepts full dependsOn configuration", () => {
    assert.deepEqual(
      normalizeDependsOn({ option: "mode", value: "dev", required: true }),
      { option: "mode", value: "dev", required: true },
    );
  });
});

describe("isDependsOnSatisfied()", () => {
  const parsers = {
    mode: option("--mode", choice(["dev", "prod"] as const)),
    enabled: option("--enabled"),
  } as const;

  const referenceMap = buildOptionReferenceMap(parsers);

  function context(
    states: Record<string, unknown>,
  ): OptionDependencyContext {
    return { states, parsers, referenceMap };
  }

  test("requires truthy value when value constraint is omitted", () => {
    const satisfied = isDependsOnSatisfied(
      { option: "enabled" },
      context({
        enabled: { success: true, value: true },
      }),
    );
    assert.ok(satisfied);

    const unsatisfied = isDependsOnSatisfied(
      { option: "enabled" },
      context({ enabled: { success: true, value: false } }),
    );
    assert.ok(!unsatisfied);
  });

  test("requires exact value when value constraint is present", () => {
    const satisfied = isDependsOnSatisfied(
      { option: "mode", value: "dev" },
      context({ mode: { success: true, value: "dev" } }),
    );
    assert.ok(satisfied);

    const unsatisfied = isDependsOnSatisfied(
      { option: "mode", value: "dev" },
      context({ mode: { success: true, value: "prod" } }),
    );
    assert.ok(!unsatisfied);
  });

  test("maps CLI flag names to object keys", () => {
    const satisfied = isDependsOnSatisfied(
      { option: "--mode", value: "prod" },
      context({ mode: { success: true, value: "prod" } }),
    );
    assert.ok(satisfied);
  });

  test("treats missing keys as unsatisfied", () => {
    const unsatisfied = isDependsOnSatisfied(
      { option: "missing" },
      context({ mode: { success: true, value: "dev" } }),
    );
    assert.ok(!unsatisfied);
  });

  test("treats empty allOf as satisfied and empty anyOf as unsatisfied", () => {
    assert.ok(isDependsOnSatisfied({ allOf: [] }, context({})));
    assert.ok(!isDependsOnSatisfied({ anyOf: [] }, context({})));
  });

  test("evaluates compound conditions", () => {
    const states = {
      mode: { success: true, value: "dev" },
      enabled: { success: true, value: true },
    };

    assert.ok(isDependsOnSatisfied({
      allOf: [{ option: "mode", value: "dev" }, { option: "enabled" }],
    }, context(states)));

    assert.ok(isDependsOnSatisfied({
      anyOf: [{ option: "mode", value: "prod" }, { option: "enabled" }],
    }, context(states)));
  });
});

function getDocOptionNames(
  parser: { getDocFragments: (state: { kind: "available"; state: unknown }) => { fragments: readonly { type: string; term?: { names?: readonly string[] } }[] } },
  state: unknown,
): string[] {
  const { fragments } = parser.getDocFragments({ kind: "available", state });
  const names: string[] = [];
  for (const fragment of fragments) {
    if (fragment.type === "entry" && fragment.term?.names) {
      names.push(...fragment.term.names);
    }
    if (fragment.type === "section" && "entries" in fragment) {
      for (const entry of (fragment as { entries: readonly { term?: { names?: readonly string[] } }[] }).entries) {
        if (entry.term?.names) {
          names.push(...entry.term.names);
        }
      }
    }
  }
  return names;
}

describe("option dependsOn integration", () => {
  test("requiredWhen fails when dependency is unsatisfied", () => {
    const parser = object({
      mode: option("--mode", choice(["dev", "prod"] as const)),
      config: requiredWhen(
        { option: "mode", value: "dev" },
        "--config",
        string(),
      ),
    });

    const result = parseSync(parser, ["--mode", "prod", "--config", "value"]);
    assert.ok(!result.success);
    assertErrorIncludes(result.error, "requires option");
    assertErrorIncludes(result.error, "--mode");
    assertErrorIncludes(result.error, "dev");
  });

  test("optionalWhen hides option from help when dependency is unsatisfied", () => {
    const parser = object({
      enabled: option("--enabled"),
      detail: optionalWhen(
        { option: "enabled" },
        "--detail",
        string(),
      ),
    });

    const names = getDocOptionNames(parser, parser.initialState);
    assert.ok(names.includes("--enabled"));
    assert.ok(!names.includes("--detail"));
  });

  test("optionalWhen allows explicit use when dependency is unsatisfied", () => {
    const parser = object({
      enabled: option("--enabled"),
      detail: optionalWhen(
        { option: "enabled" },
        "--detail",
        string(),
      ),
    });

    const result = parseSync(parser, ["--detail", "explicit"]);
    assert.ok(result.success);
    if (result.success) {
      assert.equal(result.value.detail, "explicit");
    }
  });

  test("fails when dependee is explicitly falsy and dependent option is provided", () => {
    const parser = object({
      flag: option("--flag", booleanValueParser()),
      detail: optionalWhen(
        { option: "flag" },
        "--detail",
        string(),
      ),
    });

    const result = parseSync(parser, ["--flag=false", "--detail", "value"]);
    assert.ok(!result.success);
    assertErrorIncludes(result.error, "requires option");
    assertErrorIncludes(result.error, "--flag");
  });

  test("maps CLI flag references through withDefault wrappers", () => {
    const parser = object({
      mode: withDefault(
        option("--mode", choice(["dev", "prod"] as const)),
        "dev" as const,
      ),
      config: optionalWhen(
        { option: "--mode", value: "prod" },
        "--config",
        string(),
      ),
    });

    const hiddenNames = getDocOptionNames(parser, parser.initialState);
    assert.ok(!hiddenNames.includes("--config"));

    const result = parseSync(parser, ["--mode", "prod", "--config", "x"]);
    assert.ok(result.success);
    if (result.success) {
      assert.equal(result.value.config, "x");
    }
  });

  test("conditionalOption accepts full dependsOn configuration", () => {
    const parser = object({
      mode: option("--mode", choice(["dev", "prod"] as const)),
      config: conditionalOption(
        { option: "mode", value: "dev", required: true },
        "--config",
        string(),
      ),
    });

    const fail = parseSync(parser, ["--mode", "prod", "--config", "x"]);
    assert.ok(!fail.success);
    assertErrorIncludes(fail.error, "requires option");
  });

  test("supports transitive chaining independently", () => {
    const parser = object({
      a: option("--a"),
      b: optionalWhen({ option: "a" }, "--b", string()),
      c: optionalWhen({ option: "b" }, "--c", string()),
    });

    const result = parseSync(parser, ["--a", "--b", "value", "--c", "nested"]);
    assert.ok(result.success);
    if (result.success) {
      assert.equal(result.value.c, "nested");
    }

    const names = getDocOptionNames(parser, parser.initialState);
    assert.ok(names.includes("--a"));
    assert.ok(!names.includes("--b"));
    assert.ok(!names.includes("--c"));
  });

  test("extractDependsOn reads metadata from wrapped usage", () => {
    const wrapped = withDefault(
      option("--detail", string(), {
        dependsOn: { option: "enabled", required: false },
      }),
      "default",
    );

    assert.deepEqual(extractDependsOn(wrapped.usage), {
      option: "enabled",
      required: false,
    });
  });

  test("hides dependent option from completion suggestions", () => {
    const parser = object({
      enabled: option("--enabled"),
      detail: optionalWhen(
        { option: "enabled" },
        "--detail",
        string(),
      ),
    });

    const suggestions: Suggestion[] = [];
    for (
      const suggestion of parser.suggest({
        buffer: [],
        optionsTerminated: false,
        usage: parser.usage,
        state: parser.initialState,
      }, "--")
    ) {
      suggestions.push(suggestion);
    }

    const texts = suggestions
      .filter((s) => s.kind === "literal")
      .map((s) => s.text);
    assert.ok(texts.includes("--enabled"));
    assert.ok(!texts.includes("--detail"));
  });
});
