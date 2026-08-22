import { describe, test } from "node:test";
import * as assert from "node:assert/strict";
import { object } from "./constructs.ts";
import { formatMessage } from "./message.ts";
import { parseSync } from "./parser.ts";
import {
  conditionalOption,
  flag,
  option,
  optionalWhen,
  requiredWhen,
} from "./primitives.ts";
import { optional, withDefault } from "./modifiers.ts";
import { choice, string } from "./valueparser.ts";
import {
  isDependsOnSatisfied,
  normalizeDependsOnConfig,
} from "./option-dependency.ts";
import { extractOptionNames, formatUsage } from "./usage.ts";

function helpIncludesOption(
  parser: ReturnType<typeof object>,
  state: Record<string, unknown>,
  optionName: string,
): boolean {
  const { fragments } = parser.getDocFragments({
    kind: "available",
    state,
  });
  const text = JSON.stringify(fragments);
  return text.includes(optionName);
}

describe("normalizeDependsOnConfig", () => {
  test("normalizes string condition", () => {
    assert.deepEqual(normalizeDependsOnConfig("verbose"), { option: "verbose" });
  });

  test("normalizes single condition with required flag", () => {
    assert.deepEqual(
      normalizeDependsOnConfig({ option: "mode", value: "prod" }, true),
      { option: "mode", value: "prod", required: true },
    );
  });

  test("empty allOf is satisfied", () => {
    const config = normalizeDependsOnConfig({ allOf: [] });
    const context = {
      fieldKey: "output",
      siblingStates: {},
      flagToKey: new Map(),
      keyToPrimaryFlag: new Map(),
      parsers: {},
    };
    assert.ok(isDependsOnSatisfied(config, context));
  });

  test("empty anyOf is unsatisfied", () => {
    const config = normalizeDependsOnConfig({ anyOf: [] });
    const context = {
      fieldKey: "output",
      siblingStates: {},
      flagToKey: new Map(),
      keyToPrimaryFlag: new Map(),
      parsers: {},
    };
    assert.ok(!isDependsOnSatisfied(config, context));
  });
});

describe("optionalWhen", () => {
  test("hides dependent option from help when dependency unsatisfied", () => {
    const parser = object({
      verbose: optional(flag("--verbose")),
      output: optionalWhen("verbose", "--output", string()),
    });

    const helpWithoutVerbose = helpIncludesOption(parser, {
      verbose: { success: true, value: false },
      output: undefined,
    }, "--output");
    assert.ok(!helpWithoutVerbose);

    const helpWithVerbose = helpIncludesOption(parser, {
      verbose: { success: true, value: true },
      output: undefined,
    }, "--output");
    assert.ok(helpWithVerbose);
  });

  test("allows parsing hidden option when dependency not required", () => {
    const parser = object({
      verbose: optional(flag("--verbose")),
      output: optionalWhen("verbose", "--output", string()),
    });

    const result = parseSync(parser, ["--output", "file.txt"]);
    assert.ok(result.success);
    if (result.success) {
      assert.equal(result.value.output, "file.txt");
    }
  });

  test("resolves dependency by CLI flag name", () => {
    const parser = object({
      verbose: optional(flag("--verbose")),
      output: optionalWhen("--verbose", "--output", string()),
    });

    const help = helpIncludesOption(parser, {
      verbose: { success: true, value: true },
      output: undefined,
    }, "--output");
    assert.ok(help);
  });
});

describe("requiredWhen", () => {
  test("fails when dependent provided without satisfied dependency", () => {
    const parser = object({
      mode: optional(option("--mode", choice(["dev", "prod"]))),
      output: requiredWhen(
        { option: "mode", value: "prod" },
        "--output",
        string(),
      ),
    });

    const result = parseSync(parser, ["--output", "out.txt"]);
    assert.ok(!result.success);
    if (!result.success) {
      const text = formatMessage(result.error, { colors: false });
      assert.ok(text.includes("requires option"));
      assert.ok(text.includes("--mode"));
    }
  });

  test("succeeds when dependency satisfied with expected value", () => {
    const parser = object({
      mode: option("--mode", choice(["dev", "prod"])),
      output: requiredWhen(
        { option: "mode", value: "prod" },
        "--output",
        string(),
      ),
    });

    const result = parseSync(parser, [
      "--mode",
      "prod",
      "--output",
      "out.txt",
    ]);
    assert.ok(result.success);
    if (result.success) {
      assert.equal(result.value.output, "out.txt");
    }
  });

  test("includes expected value in error message", () => {
    const parser = object({
      mode: optional(option("--mode", choice(["dev", "prod"]))),
      output: requiredWhen(
        { option: "--mode", value: "prod" },
        "--output",
        string(),
      ),
    });

    const result = parseSync(parser, ["--mode", "dev", "--output", "x"]);
    assert.ok(!result.success);
    if (!result.success) {
      const text = formatMessage(result.error, { colors: false });
      assert.ok(text.includes("requires option"));
      assert.ok(text.includes("prod"));
    }
  });
});

describe("conditionalOption with compound dependencies", () => {
  test("supports anyOf conditions", () => {
    const parser = object({
      verbose: optional(flag("--verbose")),
      debug: flag("--debug"),
      output: conditionalOption(
        { anyOf: ["verbose", "debug"] },
        "--output",
        string(),
      ),
    });

    const help = helpIncludesOption(parser, {
      verbose: { success: true, value: false },
      debug: { success: true, value: true },
      output: undefined,
    }, "--output");
    assert.ok(help);
  });

  test("supports allOf conditions", () => {
    const parser = object({
      verbose: optional(flag("--verbose")),
      mode: withDefault(option("--mode", choice(["dev", "prod"])), "dev"),
      output: conditionalOption(
        { allOf: [{ option: "verbose" }, { option: "mode", value: "prod" }] },
        "--output",
        string(),
      ),
    });

    const hidden = helpIncludesOption(parser, {
      verbose: { success: true, value: true },
      mode: undefined,
      output: undefined,
    }, "--output");
    assert.ok(!hidden);
  });
});

describe("withDefault wrapper preserves dependency metadata", () => {
  test("dependency visibility survives withDefault wrapper", () => {
    const parser = object({
      enabled: withDefault(flag("--enabled"), false),
      config: optionalWhen(
        "enabled",
        "--config",
        string(),
      ),
    });

    const hidden = helpIncludesOption(parser, {
      enabled: undefined,
      config: undefined,
    }, "--config");
    assert.ok(!hidden);

    const visible = helpIncludesOption(parser, {
      enabled: [{ success: true, value: true }],
      config: undefined,
    }, "--config");
    assert.ok(visible);
  });
});

describe("missing dependency keys", () => {
  test("treats missing dependee as unsatisfied", () => {
    const parser = object({
      output: optionalWhen("nonexistent", "--output", string()),
    });

    const help = helpIncludesOption(parser, {
      output: undefined,
    }, "--output");
    assert.ok(!help);
  });
});

describe("completion visibility", () => {
  test("hides dependent option from completion when unsatisfied", () => {
    const parser = object({
      verbose: optional(flag("--verbose")),
      output: optionalWhen("verbose", "--output", string()),
    });

    const suggestions = [
      ...parser.suggest(
        {
          buffer: ["--ou"],
          state: {
            verbose: { success: true, value: false },
            output: undefined,
          },
          optionsTerminated: false,
          usage: parser.usage,
        },
        "--ou",
      ),
    ];

    assert.ok(!suggestions.some((s) => s.kind === "literal" && s.text === "--output"));
  });

  test("shows dependent option in completion when satisfied", () => {
    const parser = object({
      verbose: optional(flag("--verbose")),
      output: optionalWhen("verbose", "--output", string()),
    });

    const suggestions = [
      ...parser.suggest(
        {
          buffer: ["--ou"],
          state: {
            verbose: { success: true, value: true },
            output: undefined,
          },
          optionsTerminated: false,
          usage: parser.usage,
        },
        "--ou",
      ),
    ];

    assert.ok(suggestions.some((s) => s.kind === "literal" && s.text === "--output"));
  });
});

describe("explicit falsy dependee", () => {
  test("fails when dependee is explicitly false and dependent is provided", () => {
    const parser = object({
      enabled: option("--enabled", choice(["true", "false"])),
      config: optionalWhen("enabled", "--config", string()),
    });

    const result = parseSync(parser, [
      "--enabled",
      "false",
      "--config",
      "value",
    ]);
    assert.ok(!result.success);
  });
});

describe("usage term stores dependsOn", () => {
  test("includes dependsOn in usage for wrapped options", () => {
    const opt = optionalWhen("verbose", "--output", string());
    const names = extractOptionNames(opt.usage);
    assert.ok(names.has("--output"));
    const usageText = formatUsage("cmd", opt.usage);
    assert.ok(usageText.includes("--output"));
  });
});
