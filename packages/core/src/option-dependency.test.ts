import { merge, object } from "@optique/core/constructs";
import { formatDocPage } from "@optique/core/doc";
import { formatMessage, message } from "@optique/core/message";
import { optional, withDefault } from "@optique/core/modifiers";
import {
  getDocPageSync,
  parseAsync,
  type Parser,
  parseSync,
  type Result,
  suggestSync,
} from "@optique/core/parser";
import {
  command,
  conditionalOption,
  option,
  optionalWhen,
  requiredWhen,
} from "@optique/core/primitives";
import type { OptionName, Usage } from "@optique/core/usage";
import {
  choice,
  integer,
  string,
  type ValueParser,
  type ValueParserResult,
} from "@optique/core/valueparser";
import assert from "node:assert/strict";
import { describe, it } from "node:test";

function boolean(): ValueParser<"sync", boolean> {
  return {
    $mode: "sync",
    metavar: "BOOLEAN",
    parse(input: string): ValueParserResult<boolean> {
      if (input === "true") return { success: true, value: true };
      if (input === "false") return { success: true, value: false };
      return { success: false, error: message`Invalid Boolean: ${input}.` };
    },
    format(value: boolean): string {
      return value ? "true" : "false";
    },
  };
}

function asyncString(): ValueParser<"async", string> {
  return {
    $mode: "async",
    metavar: "STRING",
    parse(input: string): Promise<ValueParserResult<string>> {
      return Promise.resolve({ success: true, value: input });
    },
    format(value: string): string {
      return value;
    },
  };
}

function errorText<T>(result: Result<T>): string {
  if (result.success) throw new Error("Expected parsing to fail.");
  return formatMessage(result.error);
}

function valueOf<T>(result: Result<T>): T {
  if (!result.success) throw new Error(formatMessage(result.error));
  return result.value;
}

function helpText(
  parser: Parser<"sync", unknown, unknown>,
  args: readonly string[],
): string {
  const page = getDocPageSync(parser, args);
  if (page == null) throw new Error("Expected a doc page.");
  return formatDocPage("prog", page);
}

function dependsOnOf(usage: Usage, name: string): unknown {
  for (const term of usage) {
    if (term.type === "option" && term.names.includes(name as OptionName)) {
      return term.dependsOn;
    }
  }
  return undefined;
}

function suggestionTexts(
  parser: Parser<"sync", unknown, unknown>,
  args: readonly [string, ...string[]],
): string[] {
  return suggestSync(parser, args).flatMap((s) =>
    s.kind === "literal" ? [s.text] : []
  );
}

describe("option() dependsOn", () => {
  const parser = object({
    auth: option("--auth"),
    token: option("--token", string(), { dependsOn: { option: "auth" } }),
  });

  it("should record dependsOn in the usage term", () => {
    const opt = option("--token", string(), { dependsOn: { option: "auth" } });
    assert.deepEqual(dependsOnOf(opt.usage, "--token"), { option: "auth" });
  });

  it("should parse the dependent option when the dependency is met", () => {
    assert.deepEqual(valueOf(parseSync(parser, ["--auth", "--token", "abc"])), {
      auth: true,
      token: "abc",
    });
  });

  it("should not require an inactive dependent option", () => {
    assert.deepEqual(valueOf(parseSync(parser, [])), {
      auth: false,
      token: undefined,
    });
  });

  it("should accept an explicitly given hidden dependent option", () => {
    assert.deepEqual(valueOf(parseSync(parser, ["--token", "abc"])), {
      auth: false,
      token: "abc",
    });
  });

  it("should hide the dependent option from help while unsatisfied", () => {
    const help = helpText(parser, []);
    assert.ok(help.includes("--auth"));
    assert.ok(!help.includes("--token"));
  });

  it("should show the dependent option in help once satisfied", () => {
    assert.ok(helpText(parser, ["--auth"]).includes("--token"));
  });

  it("should hide the dependent option from completion while unsatisfied", () => {
    const texts = suggestionTexts(parser, ["--"]);
    assert.ok(texts.includes("--auth"));
    assert.ok(!texts.includes("--token"));
  });

  it("should suggest the dependent option once satisfied", () => {
    assert.ok(suggestionTexts(parser, ["--auth", "--"]).includes("--token"));
  });

  it("should accept a CLI flag string as the dependency reference", () => {
    const flagParser = object({
      auth: option("-a", "--auth"),
      token: option("--token", string(), { dependsOn: { option: "-a" } }),
    });
    assert.ok(!helpText(flagParser, []).includes("--token"));
    assert.ok(helpText(flagParser, ["--auth"]).includes("--token"));
  });

  it("should map CLI flags through wrapped dependees", () => {
    const wrapped = object({
      mode: withDefault(option("--mode", string()), "basic"),
      level: option("--level", integer(), {
        dependsOn: { option: "--mode", value: "advanced", required: true },
      }),
    });
    assert.deepEqual(
      valueOf(parseSync(wrapped, ["--mode", "advanced", "--level", "3"])),
      { mode: "advanced", level: 3 },
    );
    const text = errorText(parseSync(wrapped, ["--level", "3"]));
    assert.ok(text.includes("requires option"));
    assert.ok(text.includes("--mode"));
    assert.ok(text.includes("advanced"));
  });

  it("should keep dependency metadata on wrapped dependents", () => {
    const wrapped = object({
      auth: option("--auth"),
      token: withDefault(
        option("--token", string(), { dependsOn: { option: "auth" } }),
        "none",
      ),
    });
    assert.ok(!helpText(wrapped, []).includes("--token"));
    assert.ok(!suggestionTexts(wrapped, ["--"]).includes("--token"));
    assert.ok(helpText(wrapped, ["--auth"]).includes("--token"));
    assert.deepEqual(valueOf(parseSync(wrapped, [])), {
      auth: false,
      token: "none",
    });
  });

  it("should treat unknown dependency references as unsatisfied", () => {
    const missing = object({
      token: option("--token", string(), {
        dependsOn: { option: "nonexistent" },
      }),
    });
    assert.ok(!helpText(missing, []).includes("--token"));
    assert.deepEqual(valueOf(parseSync(missing, ["--token", "abc"])), {
      token: "abc",
    });

    const requiredMissing = object({
      token: option("--token", string(), {
        dependsOn: { option: "--nope", required: true },
      }),
    });
    const text = errorText(parseSync(requiredMissing, ["--token", "abc"]));
    assert.ok(text.includes("requires option"));
    assert.ok(text.includes("--nope"));
  });
});

describe("dependsOn with required", () => {
  const parser = object({
    auth: option("--auth"),
    token: option("--token", string(), {
      dependsOn: { option: "auth", required: true },
    }),
  });

  it("should fail with the dependee's flag name", () => {
    const text = errorText(parseSync(parser, ["--token", "abc"]));
    assert.ok(text.includes("requires option"));
    assert.ok(text.includes("--auth"));
    assert.ok(text.includes("--token"));
  });

  it("should succeed when the dependency is met", () => {
    assert.deepEqual(valueOf(parseSync(parser, ["--token", "abc", "--auth"])), {
      auth: true,
      token: "abc",
    });
  });

  it("should not fail when neither option is given", () => {
    assert.deepEqual(valueOf(parseSync(parser, [])), {
      auth: false,
      token: undefined,
    });
  });

  it("should report a missing dependent once the dependency is met", () => {
    assert.ok(!parseSync(parser, ["--auth"]).success);
  });

  it("should state the expected value", () => {
    const valued = object({
      mode: option("--mode", choice(["basic", "advanced"])),
      level: optional(option("--level", integer(), {
        dependsOn: { option: "mode", value: "advanced", required: true },
      })),
    });
    const text = errorText(
      parseSync(valued, ["--mode", "basic", "--level", "1"]),
    );
    assert.ok(text.includes("requires option"));
    assert.ok(text.includes("--mode"));
    assert.ok(text.includes('"advanced"'));
    assert.deepEqual(
      valueOf(parseSync(valued, ["--mode", "advanced", "--level", "1"])),
      { mode: "advanced", level: 1 },
    );
  });
});

describe("dependsOn value semantics", () => {
  it("should only be satisfied by an equal value", () => {
    const parser = object({
      mode: optional(option("--mode", choice(["basic", "advanced"]))),
      level: optional(option("--level", integer(), {
        dependsOn: { option: "mode", value: "advanced" },
      })),
    });
    assert.ok(!helpText(parser, ["--mode", "basic"]).includes("--level"));
    assert.ok(helpText(parser, ["--mode", "advanced"]).includes("--level"));
  });

  it("should fail when the dependee is explicitly falsy", () => {
    const parser = object({
      feature: optional(option("--feature", boolean())),
      extra: optional(option("--extra", string(), {
        dependsOn: { option: "feature" },
      })),
    });
    const text = errorText(
      parseSync(parser, ["--feature=false", "--extra", "x"]),
    );
    assert.ok(text.includes("requires option"));
    assert.ok(text.includes("--feature"));
    assert.deepEqual(
      valueOf(parseSync(parser, ["--feature=true", "--extra", "x"])),
      { feature: true, extra: "x" },
    );
    assert.ok(parseSync(parser, ["--extra", "x"]).success);
  });

  it('should treat a literal "false" string as falsy', () => {
    const parser = object({
      feature: optional(option("--feature", choice(["true", "false"]))),
      extra: optional(option("--extra", string(), {
        dependsOn: { option: "--feature" },
      })),
    });
    assert.ok(
      !parseSync(parser, ["--feature", "false", "--extra", "x"]).success,
    );
    assert.ok(parseSync(parser, ["--feature", "true", "--extra", "x"]).success);
  });
});

describe("compound dependsOn", () => {
  it("should support anyOf", () => {
    const parser = object({
      a: option("-a"),
      b: option("-b"),
      c: optional(option("-c", string(), {
        dependsOn: { anyOf: [{ option: "a" }, "b"], required: true },
      })),
    });
    assert.ok(parseSync(parser, ["-a", "-c", "x"]).success);
    assert.ok(parseSync(parser, ["-b", "-c", "x"]).success);
    const text = errorText(parseSync(parser, ["-c", "x"]));
    assert.ok(text.includes("requires option"));
    assert.ok(text.includes("-a"));
    assert.ok(text.includes("-b"));
  });

  it("should support allOf", () => {
    const parser = object({
      a: option("-a"),
      b: option("-b"),
      c: optional(option("-c", string(), {
        dependsOn: { allOf: [{ option: "a" }, { option: "b" }] },
      })),
    });
    assert.ok(!helpText(parser, ["-a"]).includes("-c"));
    assert.ok(helpText(parser, ["-a", "-b"]).includes("-c"));
  });

  it("should treat empty allOf as satisfied and empty anyOf as not", () => {
    const parser = object({
      x: optional(option("-x", string(), { dependsOn: { allOf: [] } })),
      y: optional(option("-y", string(), { dependsOn: { anyOf: [] } })),
    });
    const help = helpText(parser, []);
    assert.ok(help.includes("-x"));
    assert.ok(!help.includes("-y"));
    const required = object({
      y: optional(option("-y", string(), {
        dependsOn: { anyOf: [], required: true },
      })),
    });
    assert.ok(
      errorText(parseSync(required, ["-y", "v"])).includes("requires option"),
    );
  });

  it("should evaluate transitive chains link by link", () => {
    const parser = object({
      c: option("-c"),
      b: option("-b", { dependsOn: { option: "c", required: true } }),
      a: option("-a", { dependsOn: { option: "b", required: true } }),
    });
    assert.ok(parseSync(parser, ["-c", "-b", "-a"]).success);
    assert.ok(errorText(parseSync(parser, ["-c", "-a"])).includes("-b"));
    assert.ok(errorText(parseSync(parser, ["-b", "-a"])).includes("-c"));
    const help = helpText(
      object({
        c: option("-c"),
        b: option("-b", { dependsOn: { option: "c" } }),
        a: option("-a", { dependsOn: { option: "b" } }),
      }),
      ["-c"],
    );
    assert.ok(help.includes("-b"));
    assert.ok(!help.includes("-a"));
  });
});

describe("conditional option helpers", () => {
  it("requiredWhen() should accept a string condition", () => {
    const parser = object({
      auth: option("--auth"),
      token: requiredWhen("auth", "--token", string()),
    });
    assert.deepEqual(dependsOnOf(parser.usage, "--token"), {
      option: "auth",
      required: true,
    });
    const text = errorText(parseSync(parser, ["--token", "x"]));
    assert.ok(text.includes("requires option"));
    assert.ok(text.includes("--auth"));
    assert.ok(!parseSync(parser, ["--auth"]).success);
    assert.deepEqual(valueOf(parseSync(parser, ["--auth", "--token", "x"])), {
      auth: true,
      token: "x",
    });
  });

  it("requiredWhen() should accept an object condition", () => {
    const parser = object({
      mode: optional(option("--mode", string())),
      level: requiredWhen(
        { option: "mode", value: "advanced" },
        "--level",
        integer(),
      ),
    });
    const text = errorText(parseSync(parser, ["--level", "1"]));
    assert.ok(text.includes("requires option"));
    assert.ok(text.includes("advanced"));
    assert.ok(
      parseSync(parser, ["--mode", "advanced", "--level", "1"]).success,
    );
  });

  it("optionalWhen() should hide without failing", () => {
    const parser = object({
      auth: option("--auth"),
      token: optionalWhen("auth", ["-t", "--token"], string()),
      verbose: optionalWhen({ anyOf: ["auth"] }, "--verbose"),
    });
    const help = helpText(parser, []);
    assert.ok(!help.includes("--token"));
    assert.ok(!help.includes("--verbose"));
    assert.deepEqual(valueOf(parseSync(parser, ["-t", "x"])), {
      auth: false,
      token: "x",
      verbose: false,
    });
    assert.deepEqual(dependsOnOf(parser.usage, "-t"), {
      option: "auth",
      required: false,
    });
  });

  it("conditionalOption() should accept a full dependsOn configuration", () => {
    const parser = object({
      auth: option("--auth"),
      token: conditionalOption(
        { option: "auth", required: true },
        "--token",
        string(),
      ),
      other: conditionalOption("auth", "--other", string()),
    });
    assert.ok(
      errorText(parseSync(parser, ["--token", "x"])).includes(
        "requires option",
      ),
    );
    assert.deepEqual(valueOf(parseSync(parser, ["--other", "y"])), {
      auth: false,
      token: undefined,
      other: "y",
    });
  });

  it("should forward option options such as descriptions", () => {
    const parser = object({
      auth: option("--auth"),
      token: optionalWhen("auth", "--token", string(), {
        description: message`API token.`,
      }),
    });
    assert.ok(helpText(parser, ["--auth"]).includes("API token."));
  });
});

describe("dependsOn robustness", () => {
  it("should work inside merge()", () => {
    const parser = merge(
      object({ verbose: option("-v") }),
      object({
        auth: option("--auth"),
        token: option("--token", string(), {
          dependsOn: { option: "auth", required: true },
        }),
      }),
    );
    assert.deepEqual(valueOf(parseSync(parser, [])), {
      verbose: false,
      auth: false,
      token: undefined,
    });
    assert.ok(!parseSync(parser, ["--token", "x"]).success);
    assert.ok(parseSync(parser, ["--auth", "--token", "x"]).success);
  });

  it("should work inside command()", () => {
    const parser = command(
      "login",
      object({
        auth: option("--auth"),
        token: option("--token", string(), { dependsOn: { option: "auth" } }),
      }),
    );
    assert.ok(!helpText(parser, ["login"]).includes("--token"));
    assert.ok(helpText(parser, ["login", "--auth"]).includes("--token"));
    assert.ok(parseSync(parser, ["login"]).success);
  });

  it("should work with async parsers", async () => {
    const parser = object({
      auth: option("--auth", asyncString()),
      token: option("--token", asyncString(), {
        dependsOn: { option: "auth", value: "yes", required: true },
      }),
    });
    assert.deepEqual(
      valueOf(await parseAsync(parser, ["--auth", "yes", "--token", "t"])),
      { auth: "yes", token: "t" },
    );
    const text = errorText(
      await parseAsync(parser, ["--auth", "no", "--token", "t"]),
    );
    assert.ok(text.includes("requires option"));
    assert.ok(text.includes("--auth"));
  });
});
