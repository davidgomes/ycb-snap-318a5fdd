import { describe, test } from "node:test";
import * as assert from "node:assert/strict";
import { formatMessage } from "./message.ts";
import {
  conditionalOption,
  optionalWhen,
  option,
  requiredWhen,
} from "./primitives.ts";
import { formatUsage } from "./usage.ts";
import { getDocPageSync, object, parseSync, suggestSync } from "./parser.ts";
import { string, type ValueParser } from "./valueparser.ts";
import { withDefault } from "./modifiers.ts";
import type { NonEmptyString } from "./nonempty.ts";

function boolParser(): ValueParser<"sync", boolean> {
  return {
    $mode: "sync",
    metavar: "BOOL" as NonEmptyString,
    parse(input: string) {
      if (input === "true") return { success: true, value: true };
      if (input === "false") return { success: true, value: false };
      return { success: false, error: [{ type: "text", text: "Expected a boolean." }] };
    },
    format(value: boolean) {
      return value ? "true" : "false";
    },
  };
}

describe("conditional option dependencies", () => {
  test("truthy presence satisfies a single dependency", () => {
    const parser = object({
      verbose: option("--verbose"),
      extra: option("--extra", { dependsOn: { option: "verbose" } }),
    });
    const hidden = parseSync(parser, []);
    assert.ok(hidden.success);
    if (hidden.success) {
      assert.equal(hidden.value.verbose, false);
      assert.equal(hidden.value.extra, false);
    }
    const shown = parseSync(parser, ["--verbose", "--extra"]);
    assert.ok(shown.success);
    if (shown.success) {
      assert.equal(shown.value.verbose, true);
      assert.equal(shown.value.extra, true);
    }
  });

  test("flag strings map to object keys through withDefault", () => {
    const parser = object({
      mode: withDefault(option("--mode", string({ metavar: "MODE" })), "dev"),
      pretty: option("--pretty", { dependsOn: { option: "--mode", value: "json" } }),
    });
    const unmet = parseSync(parser, ["--pretty"]);
    assert.ok(unmet.success);
    const met = parseSync(parser, ["--mode", "json", "--pretty"]);
    assert.ok(met.success);
    if (met.success) assert.equal(met.value.pretty, true);
  });

  test("required dependency names the dependee flag and expected value", () => {
    const parser = object({
      mode: option("--mode", string({ metavar: "MODE" })),
      pretty: requiredWhen(
        { option: "mode", value: "json" },
        "--pretty",
      ),
    });
    const result = parseSync(parser, ["--mode", "text"]);
    assert.ok(!result.success);
    if (!result.success) {
      const formatted = formatMessage(result.error);
      assert.ok(formatted.includes("requires option"));
      assert.ok(formatted.includes("--mode"));
      assert.ok(formatted.includes("json"));
    }
  });

  test("explicit falsy dependee rejects the dependent option", () => {
    const parser = object({
      flag: option("--flag", boolParser()),
      extra: option("--extra", string({ metavar: "EXTRA" }), {
        dependsOn: { option: "--flag" },
      }),
    });
    const result = parseSync(parser, ["--flag", "false", "--extra", "yes"]);
    assert.ok(!result.success);
    if (!result.success) {
      const formatted = formatMessage(result.error);
      assert.ok(formatted.includes("requires option"));
      assert.ok(formatted.includes("--flag"));
    }
  });

  test("unsatisfied optional dependency still parses an explicit option", () => {
    const parser = object({
      verbose: option("--verbose"),
      extra: optionalWhen("verbose", "--extra", string({ metavar: "EXTRA" })),
    });
    const result = parseSync(parser, ["--extra", "kept"]);
    assert.ok(result.success);
    if (result.success) assert.equal(result.value.extra, "kept");
  });

  test("missing dependency keys are unsatisfied", () => {
    const parser = object({
      extra: option("--extra", {
        dependsOn: { option: "--missing", required: true },
      }),
    });
    const result = parseSync(parser, ["--extra"]);
    assert.ok(!result.success);
    if (!result.success) {
      assert.ok(formatMessage(result.error).includes("requires option"));
    }
  });

  test("empty allOf is satisfied and empty anyOf is not", () => {
    const all = object({
      extra: option("--extra", { dependsOn: { allOf: [], required: true } }),
    });
    assert.ok(parseSync(all, ["--extra"]).success);

    const any = object({
      extra: option("--extra", { dependsOn: { anyOf: [], required: true } }),
    });
    const result = parseSync(any, []);
    assert.ok(!result.success);
  });

  test("transitive dependencies are evaluated independently", () => {
    const parser = object({
      a: option("--a"),
      b: option("--b", { dependsOn: { option: "a", required: true } }),
      c: option("--c", { dependsOn: { option: "b" } }),
    });
    const missingA = parseSync(parser, ["--c"]);
    assert.ok(!missingA.success);
    if (!missingA.success) {
      assert.ok(formatMessage(missingA.error).includes("--a"));
    }
    const withC = parseSync(parser, ["--a", "--c"]);
    assert.ok(withC.success);
    if (withC.success) {
      assert.equal(withC.value.b, false);
      assert.equal(withC.value.c, true);
    }
  });

  test("hides unsatisfied options from help and completion", () => {
    const parser = object({
      verbose: option("--verbose"),
      extra: option("--extra", { dependsOn: { option: "--verbose" } }),
    });
    const usage = formatUsage("cli", parser.usage);
    assert.ok(!usage.includes("--extra"));
    assert.ok(usage.includes("--verbose"));

    const idle = suggestSync(parser, ["--"]);
    assert.ok(idle.some((item) => item.text === "--verbose"));
    assert.ok(!idle.some((item) => item.text === "--extra"));

    const active = suggestSync(parser, ["--verbose", "--"]);
    assert.ok(active.some((item) => item.text === "--extra"));

    const hiddenHelp = getDocPageSync(parser, []);
    const hiddenText = JSON.stringify(hiddenHelp);
    assert.ok(!hiddenText.includes("--extra"));

    const shownHelp = getDocPageSync(parser, ["--verbose"]);
    const shownText = JSON.stringify(shownHelp);
    assert.ok(shownText.includes("--extra"));
  });

  test("conditionalOption keeps required from a full dependsOn config", () => {
    const parser = object({
      verbose: option("--verbose"),
      extra: conditionalOption(
        { option: "verbose", required: true },
        "--extra",
      ),
    });
    const result = parseSync(parser, []);
    assert.ok(!result.success);
    if (!result.success) {
      assert.ok(formatMessage(result.error).includes("--verbose"));
    }
  });

  test("compound anyOf and allOf", () => {
    const parser = object({
      color: option("--color"),
      json: option("--json"),
      pretty: option("--pretty", {
        dependsOn: {
          allOf: [{ option: "--json" }],
          anyOf: [{ option: "--color" }, { option: "--json" }],
        },
      }),
    });
    assert.ok(parseSync(parser, ["--json", "--pretty"]).success);
    assert.ok(parseSync(parser, ["--color", "--pretty"]).success);
  });
});
