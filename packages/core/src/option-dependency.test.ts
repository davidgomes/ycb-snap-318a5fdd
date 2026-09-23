import { object } from "@optique/core/constructs";
import { formatDocPage } from "@optique/core/doc";
import { formatMessage, type Message } from "@optique/core/message";
import { withDefault } from "@optique/core/modifiers";
import type { NonEmptyString } from "@optique/core/nonempty";
import { getDocPageSync, parseSync, suggestSync } from "@optique/core/parser";
import {
  conditionalOption,
  option,
  optionalWhen,
  requiredWhen,
} from "@optique/core/primitives";
import {
  string,
  type ValueParser,
  type ValueParserResult,
} from "@optique/core/valueparser";
import assert from "node:assert/strict";
import { describe, it } from "node:test";

function booleanValue(): ValueParser<"sync", boolean> {
  return {
    $mode: "sync",
    metavar: "BOOL" as NonEmptyString,
    parse(input: string): ValueParserResult<boolean> {
      if (input === "true") return { success: true, value: true };
      if (input === "false") return { success: true, value: false };
      return {
        success: false,
        error: [{ type: "text", text: "Expected boolean." }],
      };
    },
    format(value: boolean): string {
      return value ? "true" : "false";
    },
  };
}

function errorText(error: Message): string {
  return formatMessage(error);
}

describe("conditional option dependencies", () => {
  it("satisfies a truthy dependency and an equality dependency", () => {
    const parser = object({
      verbose: option("--verbose"),
      format: option("--format", string()),
      log: option("--log", string(), {
        dependsOn: { option: "verbose" },
      }),
      pretty: option("--pretty", {
        dependsOn: { option: "--format", value: "json" },
      }),
    });

    const matched = parseSync(parser, [
      "--verbose",
      "--format",
      "json",
      "--log",
      "out",
      "--pretty",
    ]);
    assert.ok(matched.success);
    if (matched.success) {
      assert.equal(matched.value.verbose, true);
      assert.equal(matched.value.format, "json");
      assert.equal(matched.value.log, "out");
      assert.equal(matched.value.pretty, true);
    }

    const wrongValue = parseSync(parser, [
      "--format",
      "text",
      "--log",
      "out",
      "--pretty",
    ]);
    assert.ok(wrongValue.success);
    if (wrongValue.success) {
      assert.equal(wrongValue.value.format, "text");
      assert.equal(wrongValue.value.pretty, true);
      assert.equal(wrongValue.value.log, "out");
    }
  });

  it("accepts a hidden option when the dependency is merely absent", () => {
    const parser = object({
      verbose: option("--verbose"),
      log: optionalWhen("--verbose", "--log", string()),
    });

    const result = parseSync(parser, ["--log", "out"]);
    assert.ok(result.success);
    if (result.success) {
      assert.equal(result.value.verbose, false);
      assert.equal(result.value.log, "out");
    }
  });

  it("rejects a dependent option when the dependee is explicitly falsy", () => {
    const parser = object({
      flag: option("--flag", booleanValue()),
      extra: option("--extra", string(), {
        dependsOn: { option: "--flag" },
      }),
    });

    const result = parseSync(parser, ["--flag=false", "--extra", "yes"]);
    assert.ok(!result.success);
    if (!result.success) {
      const text = errorText(result.error);
      assert.ok(text.includes("requires option"));
      assert.ok(text.includes("--flag"));
    }
  });

  it("reports a required dependency with the dependee flag and expected value", () => {
    const parser = object({
      format: option("--format", string()),
      pretty: requiredWhen(
        { option: "--format", value: "json" },
        "--pretty",
      ),
    });

    const missing = parseSync(parser, ["--pretty"]);
    assert.ok(!missing.success);
    if (!missing.success) {
      const text = errorText(missing.error);
      assert.ok(text.includes("requires option"));
      assert.ok(text.includes("--format"));
      assert.ok(text.includes("json"));
    }

    const matched = parseSync(parser, ["--format", "json", "--pretty"]);
    assert.ok(matched.success);
    if (matched.success) {
      assert.equal(matched.value.pretty, true);
    }
  });

  it("accepts string conditions and full dependsOn configs in the helpers", () => {
    const byString = object({
      verbose: option("--verbose"),
      log: requiredWhen("verbose", "--log", string()),
    });
    const missing = parseSync(byString, ["--log", "out"]);
    assert.ok(!missing.success);
    if (!missing.success) {
      assert.ok(errorText(missing.error).includes("--verbose"));
    }

    const byConfig = object({
      verbose: option("--verbose"),
      log: conditionalOption(
        { option: "--verbose", required: true },
        "--log",
        string(),
      ),
    });
    const failed = parseSync(byConfig, ["--log", "out"]);
    assert.ok(!failed.success);
    if (!failed.success) {
      assert.ok(errorText(failed.error).includes("requires option"));
    }
    const ok = parseSync(byConfig, ["--verbose", "--log", "out"]);
    assert.ok(ok.success);
  });

  it("treats a missing key or flag as unsatisfied", () => {
    const parser = object({
      log: option("--log", string(), {
        dependsOn: { option: "--missing", required: true },
      }),
    });
    const result = parseSync(parser, ["--log", "out"]);
    assert.ok(!result.success);
    if (!result.success) {
      const text = errorText(result.error);
      assert.ok(text.includes("requires option"));
      assert.ok(text.includes("--missing"));
    }
  });

  it("treats empty allOf as satisfied and empty anyOf as unsatisfied", () => {
    const all = object({
      extra: option("--extra", {
        dependsOn: { allOf: [], required: true },
      }),
    });
    assert.ok(parseSync(all, ["--extra"]).success);

    const any = object({
      extra: requiredWhen({ anyOf: [] }, "--extra"),
    });
    const result = parseSync(any, ["--extra"]);
    assert.ok(!result.success);
    if (!result.success) {
      assert.ok(errorText(result.error).includes("requires option"));
    }
  });

  it("evaluates chained dependencies independently", () => {
    const parser = object({
      a: option("--a"),
      b: option("--b", { dependsOn: { option: "--a" } }),
      c: option("--c", { dependsOn: { option: "--b", required: true } }),
    });

    const onlyA = parseSync(parser, ["--a", "--c"]);
    assert.ok(!onlyA.success);
    if (!onlyA.success) {
      assert.ok(errorText(onlyA.error).includes("--b"));
    }

    const both = parseSync(parser, ["--a", "--b", "--c"]);
    assert.ok(both.success);
    if (both.success) {
      assert.equal(both.value.a, true);
      assert.equal(both.value.b, true);
      assert.equal(both.value.c, true);
    }
  });

  it("resolves flag names through withDefault from the usage term", () => {
    const parser = object({
      verbose: withDefault(option("--verbose", "--debug"), false),
      log: option("--log", string(), {
        dependsOn: { option: "--debug" },
      }),
    });

    const hidden = suggestSync(parser, ["--"]);
    assert.ok(
      !hidden.some((item) => item.kind === "literal" && item.text === "--log"),
    );
    assert.ok(
      hidden.some((item) =>
        item.kind === "literal" && item.text === "--verbose"
      ),
    );

    const shown = suggestSync(parser, ["--verbose", "--"]);
    assert.ok(
      shown.some((item) => item.kind === "literal" && item.text === "--log"),
    );

    const parsed = parseSync(parser, ["--log", "out"]);
    assert.ok(parsed.success);
    if (parsed.success) {
      assert.equal(parsed.value.log, "out");
      assert.equal(parsed.value.verbose, false);
    }
  });

  it("hides unsatisfied optional dependencies from help", () => {
    const parser = object({
      verbose: option("--verbose"),
      log: withDefault(
        option("--log", string(), {
          dependsOn: { option: "verbose" },
          description: [{ type: "text", text: "Write a log." }],
        }),
        "default.log",
      ),
    });

    const hidden = getDocPageSync(parser, []);
    assert.ok(hidden != null);
    if (hidden == null) return;
    const hiddenText = formatDocPage("app", hidden);
    assert.ok(!hiddenText.includes("--log"));
    assert.ok(hiddenText.includes("--verbose"));

    const shown = getDocPageSync(parser, ["--verbose"]);
    assert.ok(shown != null);
    if (shown == null) return;
    assert.ok(formatDocPage("app", shown).includes("--log"));
  });

  it("keeps required dependencies visible and fails when they are unmet", () => {
    const parser = object({
      verbose: option("--verbose"),
      log: requiredWhen("verbose", "--log", string()),
    });

    const page = getDocPageSync(parser, []);
    assert.ok(page != null);
    if (page == null) return;
    assert.ok(formatDocPage("app", page).includes("--log"));

    const result = parseSync(parser, ["--log", "out"]);
    assert.ok(!result.success);
    if (!result.success) {
      const text = errorText(result.error);
      assert.ok(text.includes("requires option"));
      assert.ok(text.includes("--verbose"));
    }
  });

  it("combines anyOf and allOf", () => {
    const parser = object({
      json: option("--json"),
      yaml: option("--yaml"),
      color: option("--color"),
      pretty: option("--pretty", {
        dependsOn: {
          anyOf: [{ option: "--json" }, { option: "yaml" }],
          allOf: [{ option: "--color" }],
        },
      }),
    });

    assert.ok(parseSync(parser, ["--json", "--color", "--pretty"]).success);
    assert.ok(parseSync(parser, ["--yaml", "--color", "--pretty"]).success);

    const missingColor = parseSync(parser, ["--json", "--pretty"]);
    assert.ok(missingColor.success);
    if (missingColor.success) {
      assert.equal(missingColor.value.pretty, true);
      assert.equal(missingColor.value.color, false);
    }
  });

  it("does not complete undefined state while checking dependencies", () => {
    const verbose = withDefault(option("--verbose"), false);
    let completedUndefined = false;
    const guarded = {
      ...verbose,
      complete(state: unknown) {
        if (state == null) {
          completedUndefined = true;
          throw new Error("complete was called with undefined state.");
        }
        return verbose.complete(state as never);
      },
    };

    const parser = object({
      verbose: guarded,
      log: option("--log", string(), {
        dependsOn: { option: "--verbose", required: true },
      }),
    });

    const result = parseSync(parser, ["--log", "out"]);
    assert.ok(!result.success);
    assert.equal(completedUndefined, false);
  });
});
