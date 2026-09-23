import { object } from "@optique/core/constructs";
import { formatMessage } from "@optique/core/message";
import { withDefault } from "@optique/core/modifiers";
import { getDocPage, parseSync, suggestSync } from "@optique/core/parser";
import {
  conditionalOption,
  option,
  optionalWhen,
  requiredWhen,
} from "@optique/core/primitives";
import { choice, string } from "@optique/core/valueparser";
import assert from "node:assert/strict";
import { describe, it } from "node:test";

function errorText(result: ReturnType<typeof parseSync>): string {
  assert.ok(!result.success);
  return formatMessage(result.error);
}

describe("option dependsOn", () => {
  const parser = object({
    mode: option("--mode", choice(["fast", "slow"])),
    verbose: option("--verbose"),
    level: withDefault(
      option("--level", string(), { dependsOn: { option: "--verbose" } }),
      "info",
    ),
    turbo: option("--turbo", {
      dependsOn: { option: "mode", value: "fast", required: true },
    }),
  });

  it("parses when the dependency is satisfied", () => {
    const r = parseSync(parser, ["--mode", "fast", "--turbo", "--verbose"]);
    assert.ok(r.success);
    assert.ok(r.value.turbo);
  });

  it("fails for required unsatisfied dependencies", () => {
    const text = errorText(
      parseSync(parser, ["--mode", "slow", "--turbo"]),
    );
    assert.ok(text.includes("requires option"));
    assert.ok(text.includes("--mode"));
    assert.ok(text.includes("fast"));
  });

  it("allows hidden dependents when the dependee is absent", () => {
    const r = parseSync(parser, ["--mode", "fast", "--level", "debug"]);
    assert.ok(r.success);
    assert.equal(r.value.level, "debug");
  });

  it("hides unsatisfied dependents from help and suggestions", () => {
    const page = getDocPage(parser);
    const json = JSON.stringify(page?.sections);
    assert.ok(!json.includes("--level"));
    assert.ok(json.includes("--turbo"));
    const s = suggestSync(parser, ["--mode", "fast", "--"]);
    assert.ok(!s.some((x) => x.kind === "literal" && x.text === "--level"));
    const s2 = suggestSync(parser, ["--mode", "fast", "--verbose", "--"]);
    assert.ok(s2.some((x) => x.kind === "literal" && x.text === "--level"));
  });

  it("fails when the dependee is explicitly falsy", () => {
    const p = object({
      flag: option("--flag", choice(["", "yes"])),
      dep: option("--dep", { dependsOn: { option: "flag" } }),
    });
    assert.ok(errorText(parseSync(p, ["--flag=", "--dep"])).includes(
      "requires option",
    ));
  });

  it("treats missing keys as unsatisfied", () => {
    const p = object({
      dep: option("--dep", { dependsOn: { option: "nope", required: true } }),
    });
    assert.ok(errorText(parseSync(p, ["--dep"])).includes("requires option"));
  });

  it("handles empty compound arrays", () => {
    const p = object({
      a: option("-a", { dependsOn: { allOf: [], required: true } }),
      b: option("-b", { dependsOn: { anyOf: [], required: true } }),
    });
    assert.ok(parseSync(p, ["-a"]).success);
    assert.ok(!parseSync(p, ["-b"]).success);
  });

  it("supports helpers and transitive chains", () => {
    const p = object({
      c: option("-c"),
      b: requiredWhen("c", "-b"),
      a: conditionalOption({ allOf: [{ option: "-b" }], required: true }, [
        "-a",
      ]),
      out: optionalWhen({ anyOf: [{ option: "-a" }] }, "--out", string()),
    });
    assert.ok(parseSync(p, ["-c", "-b", "-a", "--out", "x"]).success);
    assert.ok(parseSync(p, []).success);
    assert.ok(errorText(parseSync(p, ["-c", "-a"])).includes("-b"));
    assert.ok(!parseSync(p, ["-b"]).success);
  });
});
