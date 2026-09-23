import {
  isBigint,
  isDate,
  isInfinite,
  isMap,
  isNaNValue,
  isRegExp,
  isSet,
  isUndefined,
  isSymbol,
  isArray,
  isError,
  isTypedArray,
  TypedArrayConstructor,
  isURL,
} from './is.js';
import { findArr } from './util.js';
import SuperJSON from './index.js';
import { processStackFrames, processStackString } from './error-stack.js';
import { sanitizeMessage } from './error-sanitizer.js';
import { NormalizedErrorStackOptions } from './error-options.js';

export type PrimitiveTypeAnnotation = 'number' | 'undefined' | 'bigint';

type LeafTypeAnnotation = PrimitiveTypeAnnotation | 'regexp' | 'Date' | 'URL';

type TypedArrayAnnotation = ['typed-array', string];
type ClassTypeAnnotation = ['class', string];
type SymbolTypeAnnotation = ['symbol', string];
type CustomTypeAnnotation = ['custom', string];

type SimpleTypeAnnotation =
  | LeafTypeAnnotation
  | 'map'
  | 'set'
  | 'Error'
  | 'Error/stack'
  | 'Error/frames';

type CompositeTypeAnnotation =
  | TypedArrayAnnotation
  | ClassTypeAnnotation
  | SymbolTypeAnnotation
  | CustomTypeAnnotation;

export type TypeAnnotation = SimpleTypeAnnotation | CompositeTypeAnnotation;

function simpleTransformation<I, O, A extends SimpleTypeAnnotation>(
  isApplicable: (v: any, superJson: SuperJSON) => v is I,
  annotation: A,
  transform: (v: I, superJson: SuperJSON) => O,
  untransform: (v: O, superJson: SuperJSON) => I
) {
  return {
    isApplicable,
    annotation,
    transform,
    untransform,
  };
}

function classFilterMatches(
  error: Error,
  options: NormalizedErrorStackOptions | undefined
): boolean {
  if (!options?.classFilter || options.classFilter.length === 0) return true;
  return options.classFilter.includes(error.name);
}

function errorAnnotation(
  error: Error,
  superJson: SuperJSON
): 'Error' | 'Error/stack' | 'Error/frames' {
  const options = superJson.errorStack;
  if (!options || options.mode === 'off') return 'Error';
  if (!classFilterMatches(error, options)) return 'Error';
  return options.mode === 'frames' ? 'Error/frames' : 'Error/stack';
}

function isAggregateError(value: unknown): value is AggregateError {
  return typeof AggregateError !== 'undefined' && value instanceof AggregateError;
}

function attachCause(
  baseError: Record<string, any>,
  error: Error,
  superJson: SuperJSON
): void {
  const options = superJson.errorStack;
  if (!options) {
    if ('cause' in error) {
      baseError.cause = error.cause;
    }
    return;
  }

  if (options.includeCauses === 'none') return;
  if (!isError(error.cause)) return;

  const state = superJson.errorCauseState;
  if (options.includeCauses === 'direct') {
    if (state?.terminal.has(error)) return;
    baseError.cause = error.cause;
    state?.terminal.add(error.cause);
    return;
  }

  const depth = state?.depth.get(error) ?? 0;
  if (depth >= options.maxCauseDepth) return;
  state?.depth.set(error.cause, depth + 1);
  baseError.cause = error.cause;
}

function serializeError(v: Error, superJson: SuperJSON): Record<string, any> {
  const options = superJson.errorStack;
  const matches = classFilterMatches(v, options);
  const annotation = errorAnnotation(v, superJson);

  const baseError: Record<string, any> = {
    name: v.name,
    message:
      options?.sanitizeMessage && matches
        ? sanitizeMessage(String(v.message ?? ''))
        : v.message,
  };

  attachCause(baseError, v, superJson);

  if (isAggregateError(v)) {
    baseError.errors = v.errors;
  }

  superJson.allowedErrorProps.forEach(prop => {
    if (prop === 'stack') {
      // Legacy: copy the raw stack. Configured `off` never emits stack data.
      // A class-filter miss skips processing and keeps the raw stack.
      if (!options) {
        baseError.stack = v.stack;
      } else if (options.mode !== 'off' && !matches) {
        baseError.stack = v.stack;
      }
      return;
    }
    if (prop === 'stackFrames') {
      if (!options) {
        baseError.stackFrames = (v as any).stackFrames;
      }
      return;
    }
    baseError[prop] = (v as any)[prop];
  });

  if (
    annotation === 'Error/stack' &&
    superJson.allowedErrorProps.includes('stack') &&
    typeof v.stack === 'string'
  ) {
    baseError.stack = processStackString(v.stack, options);
  } else if (
    annotation === 'Error/frames' &&
    superJson.allowedErrorProps.includes('stackFrames') &&
    typeof v.stack === 'string'
  ) {
    baseError.stackFrames = processStackFrames(v.stack, options);
  }

  const processor = superJson.errorClassRegistry.getProcessor(v.name);
  if (processor) {
    return processor(baseError);
  }

  return baseError;
}

function deserializeError(v: any, superJson: SuperJSON): Error {
  const e =
    v?.name === 'AggregateError'
      ? new AggregateError(v.errors ?? [], v.message, { cause: v.cause })
      : new Error(v.message, { cause: v.cause });
  e.name = v.name;
  e.stack = v.stack;

  if (Array.isArray(v.stackFrames)) {
    (e as any).stackFrames = v.stackFrames;
  }

  superJson.allowedErrorProps.forEach(prop => {
    (e as any)[prop] = v[prop];
  });

  return e;
}

const simpleRules = [
  simpleTransformation(
    isUndefined,
    'undefined',
    () => null,
    () => undefined
  ),
  simpleTransformation(
    isBigint,
    'bigint',
    v => v.toString(),
    v => {
      if (typeof BigInt !== 'undefined') {
        return BigInt(v);
      }

      console.error('Please add a BigInt polyfill.');

      return v as any;
    }
  ),
  simpleTransformation(
    isDate,
    'Date',
    v => v.toISOString(),
    v => new Date(v)
  ),

  simpleTransformation(
    (v, superJson): v is Error =>
      isError(v) && errorAnnotation(v, superJson) === 'Error/frames',
    'Error/frames',
    serializeError,
    deserializeError
  ),
  simpleTransformation(
    (v, superJson): v is Error =>
      isError(v) && errorAnnotation(v, superJson) === 'Error/stack',
    'Error/stack',
    serializeError,
    deserializeError
  ),
  simpleTransformation(isError, 'Error', serializeError, deserializeError),

  simpleTransformation(
    isRegExp,
    'regexp',
    v => '' + v,
    regex => {
      const body = regex.slice(1, regex.lastIndexOf('/'));
      const flags = regex.slice(regex.lastIndexOf('/') + 1);
      return new RegExp(body, flags);
    }
  ),

  simpleTransformation(
    isSet,
    'set',
    // (sets only exist in es6+)
    // eslint-disable-next-line es5/no-es6-methods
    v => [...v.values()],
    v => new Set(v)
  ),
  simpleTransformation(
    isMap,
    'map',
    v => [...v.entries()],
    v => new Map(v)
  ),

  simpleTransformation<number, 'NaN' | 'Infinity' | '-Infinity', 'number'>(
    (v): v is number => isNaNValue(v) || isInfinite(v),
    'number',
    v => {
      if (isNaNValue(v)) {
        return 'NaN';
      }

      if (v > 0) {
        return 'Infinity';
      } else {
        return '-Infinity';
      }
    },
    Number
  ),

  simpleTransformation<number, '-0', 'number'>(
    (v): v is number => v === 0 && 1 / v === -Infinity,
    'number',
    () => {
      return '-0';
    },
    Number
  ),

  simpleTransformation(
    isURL,
    'URL',
    v => v.toString(),
    v => new URL(v)
  ),
];

function compositeTransformation<I, O, A extends CompositeTypeAnnotation>(
  isApplicable: (v: any, superJson: SuperJSON) => v is I,
  annotation: (v: I, superJson: SuperJSON) => A,
  transform: (v: I, superJson: SuperJSON) => O,
  untransform: (v: O, a: A, superJson: SuperJSON) => I
) {
  return {
    isApplicable,
    annotation,
    transform,
    untransform,
  };
}

const symbolRule = compositeTransformation(
  (s, superJson): s is Symbol => {
    if (isSymbol(s)) {
      const isRegistered = !!superJson.symbolRegistry.getIdentifier(s);
      return isRegistered;
    }
    return false;
  },
  (s, superJson) => {
    const identifier = superJson.symbolRegistry.getIdentifier(s);
    return ['symbol', identifier!];
  },
  v => v.description,
  (_, a, superJson) => {
    const value = superJson.symbolRegistry.getValue(a[1]);
    if (!value) {
      throw new Error('Trying to deserialize unknown symbol');
    }
    return value;
  }
);

const constructorToName = [
  Int8Array,
  Uint8Array,
  Int16Array,
  Uint16Array,
  Int32Array,
  Uint32Array,
  Float32Array,
  Float64Array,
  Uint8ClampedArray,
].reduce<Record<string, TypedArrayConstructor>>((obj, ctor) => {
  obj[ctor.name] = ctor;
  return obj;
}, {});

const typedArrayRule = compositeTransformation(
  isTypedArray,
  v => ['typed-array', v.constructor.name],
  v => [...v],
  (v, a) => {
    const ctor = constructorToName[a[1]];

    if (!ctor) {
      throw new Error('Trying to deserialize unknown typed array');
    }

    return new ctor(v);
  }
);

export function isInstanceOfRegisteredClass(
  potentialClass: any,
  superJson: SuperJSON
): potentialClass is any {
  if (potentialClass?.constructor) {
    const isRegistered = !!superJson.classRegistry.getIdentifier(
      potentialClass.constructor
    );
    return isRegistered;
  }
  return false;
}

const classRule = compositeTransformation(
  isInstanceOfRegisteredClass,
  (clazz, superJson) => {
    const identifier = superJson.classRegistry.getIdentifier(clazz.constructor);
    return ['class', identifier!];
  },
  (clazz, superJson) => {
    const allowedProps = superJson.classRegistry.getAllowedProps(
      clazz.constructor
    );
    if (!allowedProps) {
      return { ...clazz };
    }

    const result: any = {};
    allowedProps.forEach(prop => {
      result[prop] = clazz[prop];
    });
    return result;
  },
  (v, a, superJson) => {
    const clazz = superJson.classRegistry.getValue(a[1]);

    if (!clazz) {
      throw new Error(
        `Trying to deserialize unknown class '${a[1]}' - check https://github.com/blitz-js/superjson/issues/116#issuecomment-773996564`
      );
    }

    return Object.assign(Object.create(clazz.prototype), v);
  }
);

const customRule = compositeTransformation(
  (value, superJson): value is any => {
    return !!superJson.customTransformerRegistry.findApplicable(value);
  },
  (value, superJson) => {
    const transformer = superJson.customTransformerRegistry.findApplicable(
      value
    )!;
    return ['custom', transformer.name];
  },
  (value, superJson) => {
    const transformer = superJson.customTransformerRegistry.findApplicable(
      value
    )!;
    return transformer.serialize(value);
  },
  (v, a, superJson) => {
    const transformer = superJson.customTransformerRegistry.findByName(a[1]);
    if (!transformer) {
      throw new Error('Trying to deserialize unknown custom value');
    }
    return transformer.deserialize(v);
  }
);

const compositeRules = [classRule, symbolRule, customRule, typedArrayRule];

export const transformValue = (
  value: any,
  superJson: SuperJSON
): { value: any; type: TypeAnnotation } | undefined => {
  const applicableCompositeRule = findArr(compositeRules, rule =>
    rule.isApplicable(value, superJson)
  );
  if (applicableCompositeRule) {
    return {
      value: applicableCompositeRule.transform(value as never, superJson),
      type: applicableCompositeRule.annotation(value, superJson),
    };
  }

  const applicableSimpleRule = findArr(simpleRules, rule =>
    rule.isApplicable(value, superJson)
  );

  if (applicableSimpleRule) {
    return {
      value: applicableSimpleRule.transform(value as never, superJson),
      type: applicableSimpleRule.annotation,
    };
  }

  return undefined;
};

const simpleRulesByAnnotation: Record<string, typeof simpleRules[0]> = {};
simpleRules.forEach(rule => {
  simpleRulesByAnnotation[rule.annotation] = rule;
});

export const untransformValue = (
  json: any,
  type: TypeAnnotation,
  superJson: SuperJSON
) => {
  if (isArray(type)) {
    switch (type[0]) {
      case 'symbol':
        return symbolRule.untransform(json, type, superJson);
      case 'class':
        return classRule.untransform(json, type, superJson);
      case 'custom':
        return customRule.untransform(json, type, superJson);
      case 'typed-array':
        return typedArrayRule.untransform(json, type, superJson);
      default:
        throw new Error('Unknown transformation: ' + type);
    }
  } else {
    const transformation = simpleRulesByAnnotation[type];
    if (!transformation) {
      throw new Error('Unknown transformation: ' + type);
    }

    return transformation.untransform(json as never, superJson);
  }
};
