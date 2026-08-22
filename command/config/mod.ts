export {
  ConfigParseError,
  ConfigValidationError,
  type ConfigOptions,
  type ConfigParser,
} from "./types.ts";

export function parseRc(content: string): Record<string, unknown> {
  const result: Record<string, unknown> = {};
  for (const [index, line] of content.split(/\r?\n/).entries()) {
    const trimmed = line.trim();
    if (!trimmed || trimmed.startsWith("#")) continue;
    const match = trimmed.match(/^([^=]+)=(.*)$/);
    if (!match) throw new Error(`Invalid RC configuration on line ${index + 1}.`);
    let value = match[2].trim();
    if (value.startsWith('"') && value.endsWith('"')) {
      value = value.slice(1, -1);
    } else if (value === "true" || value === "false") {
      result[match[1].trim()] = value === "true";
      continue;
    } else if (/^-?(?:\d+\.?\d*|\.\d+)$/.test(value)) {
      result[match[1].trim()] = Number(value);
      continue;
    }
    result[match[1].trim()] = value;
  }
  return result;
}

export function flattenConfig(
  value: Record<string, unknown>,
  prefix = "",
  result: Record<string, unknown> = {},
): Record<string, unknown> {
  for (const [key, child] of Object.entries(value)) {
    const name = key.replace(/-([a-z])/g, (_, c) => c.toUpperCase());
    const path = prefix ? `${prefix}.${name}` : name;
    if (
      child && typeof child === "object" && !Array.isArray(child)
    ) {
      flattenConfig(child as Record<string, unknown>, path, result);
    } else {
      result[path] = child;
    }
  }
  return result;
}
