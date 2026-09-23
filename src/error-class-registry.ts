export type ErrorStackProcessor = (serialized: Record<string, any>) => any;

export class ErrorClassRegistry {
  private readonly processors = new Map<string, ErrorStackProcessor>();

  register(name: string, fn: ErrorStackProcessor): void {
    this.processors.set(name, fn);
  }

  has(name: string): boolean {
    return this.processors.has(name);
  }

  getProcessor(name: string): ErrorStackProcessor | undefined {
    return this.processors.get(name);
  }
}
