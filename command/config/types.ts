export type ConfigParser = (content: string) => Record<string, unknown>;

export interface ConfigOptions {
  name: string;
  searchPaths?: string[];
  formats?: string[];
  mergeConfigs?: boolean;
  parser?: ConfigParser;
}

export class ConfigParseError extends Error {
  constructor(message: string, public readonly path?: string) {
    super(message);
    Object.setPrototypeOf(this, ConfigParseError.prototype);
  }
}

export class ConfigValidationError extends Error {
  constructor(message: string, public readonly path?: string) {
    super(message);
    Object.setPrototypeOf(this, ConfigValidationError.prototype);
  }
}
