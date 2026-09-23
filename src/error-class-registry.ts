export type SerializedErrorPlain = {
  name: string;
  message: string;
  stack?: string;
  stackFrames?: { raw: string }[];
  cause?: unknown;
  errors?: unknown;
  [key: string]: unknown;
};

export type Processor = (
  serialized: SerializedErrorPlain
) => SerializedErrorPlain;

export class ErrorClassRegistry {
  private readonly processors = new Map<string, Processor>();

  register(name: string, fn: Processor): void {
    this.processors.set(name, fn);
  }

  has(name: string): boolean {
    return this.processors.has(name);
  }

  getProcessor(name: string): Processor | undefined {
    return this.processors.get(name);
  }
}
