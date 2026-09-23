import { object } from "./constructs.ts";
import { type DocPage, formatDocPage } from "./doc.ts";
import { formatMessage } from "./message.ts";
import { optional, withDefault } from "./modifiers.ts";
import {
  getDocPage,
  parseSync,
  type Suggestion,
  suggestSync,
} from "./parser.ts";
import {
  conditionalOption,
  option,
  optionalWhen,
  requiredWhen,
} from "./primitives.ts";
import { choice, integer, string } from "./valueparser.ts";
import assert from "node:assert/strict";
import { describe, it } from "node:test";
import { message } from "./message.ts";
import {
  catalogFromFields,
  dependencySatisfied,
  readOptionState,
} from "./option-dependency.ts";
import type { ValueParser, ValueParserResult } from "./valueparser.ts";

function pageText(page: DocPage | undefined): string {
  if (page == null) throw new Error("Expected a documentation page.");
  return formatDocPage("tool", page);
}

function hasSuggestion(items: readonly Suggestion[], text: string): boolean {
  return items.some((item) => item.kind === "literal" && item.text === text);
}

function booleanValue(): ValueParser<"sync", boolean> {
  return {
    $mode: "sync",
    metavar: "BOOL",
    parse(input: string): ValueParserResult<boolean> {
      if (input === "true") return { success: true, value: true };
      if (input === "false") return { success: true, value: false };
      return { success: false, error: message`Expected true or false.` };
    },
    format(value: boolean): string {
      return value ? "true" : "false";
    },
  };
}

describe("conditional option dependencies", () => {
  it("satisfies a dependency when the referenced option is truthy", () => {
    const parser = object({
      verbose: option("--verbose"),
      debug: option("--debug", { dependsOn: { option: "verbose" } }),
    });

    const enabled = parseSync(parser, ["--verbose", "--debug"]);
    assert.ok(enabled.success);
    if (enabled.success) {
      assert.equal(enabled.value.verbose, true);
      assert.equal(enabled.value.debug, true);
    }

    const absent = parseSync(parser, ["--debug"]);
    assert.ok(absent.success);
    if (absent.success) {
      assert.equal(absent.value.debug, true);
      assert.equal(absent.value.verbose, false);
    }
  });

  it("satisfies a dependency only when the value matches", () => {
    const parser = object({
      format: option("--format", choice(["json", "text"])),
      pretty: optional(
        option("--pretty", {
          dependsOn: { option: "format", value: "json" },
        }),
      ),
    });

    const matched = parseSync(parser, ["--format", "json", "--pretty"]);
    assert.ok(matched.success);
    if (matched.success) assert.equal(matched.value.pretty, true);

    const different = parseSync(parser, ["--format", "text", "--pretty"]);
    assert.ok(different.success);
    if (different.success) assert.equal(different.value.pretty, true);
  });

  it("rejects a required dependency that is not satisfied", () => {
    const parser = object({
      input: option("--input", "-i", string()),
      output: optional(
        option("--output", string(), {
          dependsOn: { option: "input", required: true },
        }),
      ),
    });

    const result = parseSync(parser, ["--output", "out.txt"]);
    assert.equal(result.success, false);
    if (!result.success) {
      const text = formatMessage(result.error);
      assert.ok(text.includes("requires option"));
      assert.ok(text.includes("--input"));
    }
  });

  it("states the expected value when a required constraint fails", () => {
    const parser = object({
      format: option("-f", "--format", string()),
      indent: optional(
        requiredWhen(
          { option: "--format", value: "json" },
          "--indent",
          integer(),
        ),
      ),
    });

    const result = parseSync(parser, ["--indent", "2", "--format", "text"]);
    assert.equal(result.success, false);
    if (!result.success) {
      const text = formatMessage(result.error);
      assert.ok(text.includes("requires option"));
      assert.ok(text.includes("--format"));
      assert.ok(text.includes("json"));
    }
  });

  it("treats an explicit falsy value as an unsatisfied dependency", () => {
    const parser = object({
      flag: optional(option("--flag", booleanValue())),
      extra: optional(
        option("--extra", string(), { dependsOn: { option: "flag" } }),
      ),
    });

    const omitted = parseSync(parser, ["--extra", "yes"]);
    assert.ok(omitted.success);

    const enabled = parseSync(parser, ["--flag=true", "--extra", "yes"]);
    assert.ok(enabled.success);

    const disabled = parseSync(parser, ["--flag=false", "--extra", "yes"]);
    assert.equal(disabled.success, false);
    if (!disabled.success) {
      const text = formatMessage(disabled.error);
      assert.ok(text.includes("requires option"));
      assert.ok(text.includes("--flag"));
    }
  });

  it("treats a missing key or flag as unsatisfied", () => {
    const parser = object({
      extra: option("--extra", { dependsOn: { option: "missing" } }),
    });
    const result = parseSync(parser, ["--extra"]);
    assert.ok(result.success);

    const required = object({
      extra: requiredWhen("missing", "--extra"),
    });
    const failed = parseSync(required, ["--extra"]);
    assert.equal(failed.success, false);
    if (!failed.success) {
      assert.ok(formatMessage(failed.error).includes("requires option"));
    }
  });

  it("hides an unsatisfied optional dependency from help and completion", () => {
    const parser = object({
      verbose: option("--verbose", { description: message`Verbose.` }),
      debug: option("--debug", {
        description: message`Debug.`,
        dependsOn: { option: "--verbose" },
      }),
    });

    const hiddenText = pageText(getDocPage(parser, []));
    assert.ok(hiddenText.includes("--verbose"));
    assert.ok(!hiddenText.includes("--debug"));

    assert.ok(pageText(getDocPage(parser, ["--verbose"])).includes("--debug"));

    const hiddenSuggestions = suggestSync(parser, ["--"]);
    assert.ok(hasSuggestion(hiddenSuggestions, "--verbose"));
    assert.ok(!hasSuggestion(hiddenSuggestions, "--debug"));

    const shownSuggestions = suggestSync(parser, ["--verbose", "--"]);
    assert.ok(hasSuggestion(shownSuggestions, "--debug"));
  });

  it("keeps required dependencies visible when they are unsatisfied", () => {
    const parser = object({
      input: option("--input"),
      output: requiredWhen("input", "--output"),
    });
    const text = pageText(getDocPage(parser, []));
    assert.ok(text.includes("--output"));
    assert.ok(text.includes("--input"));
  });

  it("resolves flag names through withDefault from the usage term", () => {
    const parser = object({
      mode: withDefault(
        option("--mode", "-m", choice(["dev", "prod"])),
        "prod",
      ),
      extra: withDefault(
        option("--extra", string(), {
          dependsOn: { option: "--mode", value: "dev" },
        }),
        "none",
      ),
    });

    assert.ok(!pageText(getDocPage(parser, [])).includes("--extra"));
    assert.ok(
      pageText(getDocPage(parser, ["--mode", "dev"])).includes("--extra"),
    );

    const parsed = parseSync(parser, ["--mode", "dev", "--extra", "logs"]);
    assert.ok(parsed.success);
    if (parsed.success) assert.equal(parsed.value.extra, "logs");
  });

  it("evaluates compound dependencies", () => {
    const parser = object({
      json: option("--json"),
      color: option("--color"),
      pretty: optionalWhen(
        { allOf: [{ option: "json" }], anyOf: [{ option: "color" }] },
        "--pretty",
      ),
    });

    assert.ok(
      pageText(getDocPage(parser, ["--json", "--color"])).includes("--pretty"),
    );
    assert.ok(
      !pageText(getDocPage(parser, ["--json"])).includes("--pretty"),
    );

    const parsed = parseSync(parser, ["--json", "--color", "--pretty"]);
    assert.ok(parsed.success);
  });

  it("treats empty allOf as satisfied and empty anyOf as unsatisfied", () => {
    const fields = [
      ["always", option("--always", { dependsOn: { allOf: [] } })] as const,
      ["never", option("--never", { dependsOn: { anyOf: [] } })] as const,
    ];
    const catalog = catalogFromFields(fields, {
      always: { success: true, value: false },
      never: { success: true, value: false },
    });
    assert.equal(
      dependencySatisfied({ allOf: [] }, catalog),
      true,
    );
    assert.equal(
      dependencySatisfied({ anyOf: [] }, catalog),
      false,
    );
    assert.equal(
      dependencySatisfied({ allOf: [], anyOf: [] }, catalog),
      false,
    );

    const parser = object({
      always: option("--always", { dependsOn: { allOf: [] } }),
      never: option("--never", { dependsOn: { anyOf: [] } }),
    });
    const text = pageText(getDocPage(parser, []));
    assert.ok(text.includes("--always"));
    assert.ok(!text.includes("--never"));
  });

  it("evaluates each link in a chain on its own", () => {
    const parser = object({
      c: option("--c"),
      b: option("--b", { dependsOn: { option: "c" } }),
      a: option("--a", { dependsOn: { option: "b" } }),
    });

    const result = parseSync(parser, ["--b", "--a"]);
    assert.ok(result.success);
    if (result.success) {
      assert.equal(result.value.a, true);
      assert.equal(result.value.b, true);
      assert.equal(result.value.c, false);
    }

    const text = pageText(getDocPage(parser, ["--c"]));
    assert.ok(text.includes("--b"));
    assert.ok(!text.includes("--a"));
  });

  it("accepts string and object conditions on the helpers", () => {
    const byString = object({
      mode: option("--mode"),
      debug: requiredWhen("mode", ["--debug", "-d"]),
    });
    const missing = parseSync(byString, ["--debug"]);
    assert.equal(missing.success, false);
    if (!missing.success) {
      assert.ok(formatMessage(missing.error).includes("--mode"));
    }

    const byObject = object({
      format: option("--format", string()),
      pretty: optional(
        conditionalOption(
          { option: "format", value: "json", required: true },
          "--pretty",
        ),
      ),
    });
    const failed = parseSync(byObject, ["--pretty", "--format", "text"]);
    assert.equal(failed.success, false);
    if (!failed.success) {
      const text = formatMessage(failed.error);
      assert.ok(text.includes("requires option"));
      assert.ok(text.includes("json"));
    }

    const optionalParser = object({
      mode: option("--mode"),
      trace: optionalWhen({ option: "mode", required: true }, "--trace"),
    });
    const allowed = parseSync(optionalParser, ["--trace"]);
    assert.ok(allowed.success);
  });

  it("reads wrapped parser states and plain values", () => {
    const wrapped = readOptionState([{ success: true, value: false }], true);
    assert.equal(wrapped.provided, true);
    assert.equal(wrapped.value, false);
    assert.equal(wrapped.explicitFalsy, true);

    const plain = readOptionState(false, true);
    assert.equal(plain.explicitFalsy, true);

    const flag = readOptionState({ success: true, value: false }, false);
    assert.equal(flag.provided, false);
    assert.equal(flag.explicitFalsy, false);

    const nested = readOptionState(
      [[{ success: true, value: "json" }]],
      true,
    );
    assert.equal(nested.provided, true);
    assert.equal(nested.value, "json");
  });

  it("does not complete parsers while evaluating dependencies", () => {
    let completed = false;
    const parser = object({
      extra: option("--extra", {
        dependsOn: { option: "absent", required: true },
      }),
      other: {
        $mode: "sync" as const,
        $valueType: [] as readonly unknown[],
        $stateType: [] as readonly unknown[],
        priority: 0,
        usage: [],
        initialState: undefined,
        parse() {
          return {
            success: false as const,
            consumed: 0,
            error: message`no`,
          };
        },
        complete(state: unknown) {
          completed = true;
          if (state === undefined) {
            throw new Error("complete called with undefined state.");
          }
          return { success: true as const, value: state };
        },
        suggest() {
          return [];
        },
        getDocFragments() {
          return { fragments: [] };
        },
      },
    });

    const result = parseSync(parser, ["--extra"]);
    assert.equal(result.success, false);
    if (!result.success) {
      assert.ok(formatMessage(result.error).includes("requires option"));
    }
    assert.equal(completed, false);
  });
});
