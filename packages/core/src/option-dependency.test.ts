import { object } from "@optique/core/constructs";
import { formatDocPage } from "@optique/core/doc";
import { formatMessage, message } from "@optique/core/message";
import { withDefault } from "@optique/core/modifiers";
import {
  command,
  conditionalOption,
  option,
  optionalWhen,
  requiredWhen,
} from "@optique/core/primitives";
import {
  getDocPage,
  type Parser,
  parseSync,
  type Suggestion,
  suggestSync,
} from "@optique/core/parser";
import { choice, string, type ValueParser } from "@optique/core/valueparser";
import assert from "node:assert/strict";
import { describe, it } from "node:test";

function boolParser(): ValueParser<"sync", boolean> {
  return {
    $mode: "sync",
    metavar: "BOOL",
    parse(input) {
      if (input === "true" || input === "false") {
        return { success: true, value: input === "true" };
      }
      return { success: false, error: message`Expected a Boolean.` };
    },
    format(value) {
      return value ? "true" : "false";
    },
  };
}

function helpText(
  parser: Parser<"sync", unknown, unknown>,
  args: readonly string[] = [],
): string {
  const page = getDocPage(parser, args);
  if (page == null) {
    throw new Error("Expected a documentation page.");
  }
  return formatDocPage("app", page);
}

function suggestionTexts(
  parser: Parser<"sync", unknown, unknown>,
  args: readonly [string, ...string[]],
): string[] {
  return suggestSync(parser, args).flatMap((suggestion: Suggestion) =>
    suggestion.kind === "literal" ? [suggestion.text] : []
  );
}

function errorText(
  error: { readonly success: false; readonly error: unknown },
): string {
  return formatMessage(error.error as never);
}

describe("conditional option dependencies", () => {
  it("hides an option until its dependency is truthy", () => {
    const parser = object({
      verbose: option("-v", "--verbose"),
      color: option("--color", string(), { dependsOn: { option: "verbose" } }),
    });

    assert.equal(helpText(parser).includes("--color"), false);
    assert.equal(helpText(parser, ["--verbose"]).includes("--color"), true);
    assert.equal(suggestionTexts(parser, ["--"]).includes("--color"), false);
    assert.equal(
      suggestionTexts(parser, ["--verbose", "--"]).includes("--color"),
      true,
    );

    const hidden = parseSync(parser, ["--color", "auto"]);
    assert.equal(hidden.success, true);
    if (hidden.success) {
      assert.equal(hidden.value.color, "auto");
      assert.equal(hidden.value.verbose, false);
    }

    const shown = parseSync(parser, ["--verbose", "--color", "auto"]);
    assert.equal(shown.success, true);
    if (shown.success) {
      assert.equal(shown.value.verbose, true);
      assert.equal(shown.value.color, "auto");
    }
  });

  it("maps a CLI flag to the object key through wrappers", () => {
    const parser = object({
      verbose: withDefault(option("-v", "--verbose"), false),
      color: withDefault(
        option("--color", string(), { dependsOn: { option: "--verbose" } }),
        "never",
      ),
    });

    assert.equal(helpText(parser).includes("--color"), false);
    assert.equal(helpText(parser, ["--verbose"]).includes("--color"), true);

    const provided = parseSync(parser, ["--color", "always"]);
    assert.equal(provided.success, true);
    if (provided.success) {
      assert.equal(provided.value.color, "always");
      assert.equal(provided.value.verbose, false);
    }
  });

  it("rejects an explicitly falsy dependee", () => {
    const parser = object({
      flag: withDefault(option("--flag", boolParser()), false),
      extra: option("--extra", { dependsOn: { option: "--flag" } }),
    });

    const absent = parseSync(parser, ["--extra"]);
    assert.equal(absent.success, true);
    if (absent.success) {
      assert.equal(absent.value.flag, false);
      assert.equal(absent.value.extra, true);
    }

    const enabled = parseSync(parser, ["--flag=true", "--extra"]);
    assert.equal(enabled.success, true);

    const disabled = parseSync(parser, ["--flag=false", "--extra"]);
    assert.equal(disabled.success, false);
    if (!disabled.success) {
      const text = formatMessage(disabled.error);
      assert.equal(text.includes("requires option"), true);
      assert.equal(text.includes("--flag"), true);
    }

    const onlyFlag = parseSync(parser, ["--flag=false"]);
    assert.equal(onlyFlag.success, true);

    const requiredFlag = object({
      flag: option("--flag", boolParser()),
      extra: option("--extra", { dependsOn: { option: "--flag" } }),
    });
    const missingFlag = parseSync(requiredFlag, ["--extra"]);
    assert.equal(missingFlag.success, false);
  });

  it("requires a matching value when required is set", () => {
    const parser = object({
      format: option("--format", choice(["json", "text"])),
      pretty: option("--pretty", {
        dependsOn: { option: "format", value: "json", required: true },
      }),
    });

    const wrong = parseSync(parser, ["--format", "text", "--pretty"]);
    assert.equal(wrong.success, false);
    if (!wrong.success) {
      const text = formatMessage(wrong.error);
      assert.equal(text.includes("requires option"), true);
      assert.equal(text.includes("--format"), true);
      assert.equal(text.includes("json"), true);
    }

    const missing = parseSync(parser, ["--pretty"]);
    assert.equal(missing.success, false);
    if (!missing.success) {
      const text = formatMessage(missing.error);
      assert.equal(text.includes("requires option"), true);
      assert.equal(text.includes("--format"), true);
      assert.equal(text.includes("json"), true);
    }

    const matched = parseSync(parser, ["--format", "json", "--pretty"]);
    assert.equal(matched.success, true);
  });

  it("treats a missing key as an unsatisfied dependency", () => {
    const hidden = object({
      color: option("--color", { dependsOn: { option: "--missing" } }),
    });
    assert.equal(helpText(hidden).includes("--color"), false);
    assert.equal(parseSync(hidden, ["--color"]).success, true);

    const required = object({
      color: option("--color", {
        dependsOn: { option: "--missing", required: true },
      }),
    });
    const result = parseSync(required, ["--color"]);
    assert.equal(result.success, false);
    if (!result.success) {
      const text = formatMessage(result.error);
      assert.equal(text.includes("requires option"), true);
      assert.equal(text.includes("--missing"), true);
    }
  });

  it("treats empty allOf as satisfied and empty anyOf as unsatisfied", () => {
    const all = object({
      color: option("--color", { dependsOn: { allOf: [] } }),
    });
    assert.equal(helpText(all).includes("--color"), true);

    const any = object({
      color: option("--color", { dependsOn: { anyOf: [] } }),
    });
    assert.equal(helpText(any).includes("--color"), false);
  });

  it("evaluates compound conditions and transitive links independently", () => {
    const compound = object({
      json: option("--json"),
      yaml: option("--yaml"),
      pretty: option("--pretty", {
        dependsOn: {
          anyOf: [{ option: "json" }, { option: "--yaml" }],
        },
      }),
    });
    assert.equal(helpText(compound).includes("--pretty"), false);
    assert.equal(helpText(compound, ["--json"]).includes("--pretty"), true);
    assert.equal(helpText(compound, ["--yaml"]).includes("--pretty"), true);

    const both = object({
      json: option("--json"),
      yaml: option("--yaml"),
      strict: option("--strict", {
        dependsOn: { allOf: [{ option: "json" }, { option: "yaml" }] },
      }),
    });
    assert.equal(helpText(both, ["--json"]).includes("--strict"), false);
    assert.equal(
      helpText(both, ["--json", "--yaml"]).includes("--strict"),
      true,
    );

    const chain = object({
      c: option("--c"),
      b: option("--b", { dependsOn: { option: "c" } }),
      a: option("--a", { dependsOn: { option: "b" } }),
    });
    const onlyB = helpText(chain, ["--b"]);
    assert.equal(onlyB.includes("--a"), true);
    assert.equal(onlyB.includes("--b"), false);
    const bothLinks = helpText(chain, ["--c", "--b"]);
    assert.equal(bothLinks.includes("--a"), true);
    assert.equal(bothLinks.includes("--b"), true);
  });

  it("accepts string and object conditions from the helpers", () => {
    const colors = object({
      verbose: option("--verbose"),
      color: requiredWhen("verbose", "--color", string()),
      debug: conditionalOption(
        { option: "--verbose", required: true },
        "--debug",
      ),
      extra: optionalWhen({ anyOf: [{ option: "verbose" }] }, "--extra"),
    });

    const missingColor = parseSync(colors, ["--color", "auto"]);
    assert.equal(missingColor.success, false);
    if (!missingColor.success) {
      const text = formatMessage(missingColor.error);
      assert.equal(text.includes("requires option"), true);
      assert.equal(text.includes("--verbose"), true);
    }

    const withVerbose = parseSync(colors, ["--verbose", "--color", "auto"]);
    assert.equal(withVerbose.success, true);

    const optionalExtra = parseSync(colors, ["--extra"]);
    assert.equal(optionalExtra.success, true);

    const debug = parseSync(colors, ["--debug"]);
    assert.equal(debug.success, false);
    if (!debug.success) {
      assert.equal(
        formatMessage(debug.error).includes("requires option"),
        true,
      );
    }

    const modes = object({
      mode: option("--mode", choice(["json", "text"])),
      pretty: requiredWhen(
        { option: "mode", value: "json" },
        ["-p", "--pretty"],
      ),
    });
    const wrongMode = parseSync(modes, ["--mode", "text", "--pretty"]);
    assert.equal(wrongMode.success, false);
    if (!wrongMode.success) {
      const text = formatMessage(wrongMode.error);
      assert.equal(text.includes("requires option"), true);
      assert.equal(text.includes("--mode"), true);
      assert.equal(text.includes("json"), true);
    }
  });

  it("keeps conditional options out of command help until satisfied", () => {
    const parser = command(
      "build",
      object({
        verbose: option("--verbose"),
        color: option("--color", { dependsOn: { option: "verbose" } }),
      }),
    );
    assert.equal(helpText(parser, ["build"]).includes("--color"), false);
    assert.equal(
      helpText(parser, ["build", "--verbose"]).includes("--color"),
      true,
    );
  });

  it("does not complete undefined parsers while checking dependencies", () => {
    let undefinedCompletes = 0;
    const sentinel = { tag: "initial" };
    const guarded = {
      $mode: "sync" as const,
      $valueType: [] as unknown[],
      $stateType: [] as unknown[],
      priority: 1,
      usage: [],
      initialState: sentinel,
      parse() {
        return {
          success: false as const,
          consumed: 0,
          error: message`no match.`,
        };
      },
      complete(state: unknown) {
        if (state === undefined) {
          undefinedCompletes += 1;
          throw new TypeError("complete called with undefined state.");
        }
        return { success: true as const, value: null };
      },
      suggest() {
        return [];
      },
      getDocFragments() {
        return { fragments: [] };
      },
    };
    const fields: Record<string, Parser<"sync", unknown, unknown> | undefined> =
      {
        guarded,
        extra: option("--extra", { dependsOn: { option: "guarded" } }),
        ghost: undefined,
      };
    const parser = object(
      fields as {
        guarded: Parser<"sync", unknown, unknown>;
        extra: Parser<"sync", unknown, unknown>;
      },
    );

    assert.doesNotThrow(() => getDocPage(parser, []));
    assert.doesNotThrow(() => suggestSync(parser, ["--"]));
    const parsed = parseSync(parser, ["--extra"]);
    assert.equal(parsed.success, true);
    assert.equal(undefinedCompletes, 0);
  });

  it("reads plain and wrapped field values", () => {
    const parser = object({
      flag: option("--flag", boolParser()),
      extra: option("--extra", { dependsOn: { option: "flag" } }),
    });
    const plain = parser.complete({
      flag: false,
      extra: true,
    } as never);
    assert.equal(plain.success, false);
    if (!plain.success) {
      assert.equal(errorText(plain).includes("requires option"), true);
    }

    const wrapped = parser.complete({
      flag: [{ success: true, value: false }],
      extra: { success: true, value: true },
    } as never);
    assert.equal(wrapped.success, false);
  });
});
