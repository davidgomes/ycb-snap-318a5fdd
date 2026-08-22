export type Processor = (error: Record<string, any>) => Record<string, any>;

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
