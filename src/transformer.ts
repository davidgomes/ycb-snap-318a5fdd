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
  isPlainObject,
} from './is.js';
import { findArr } from './util.js';
import SuperJSON from './index.js';
import type { NormalizedErrorStackOptions } from './error-options.js';
import type { SerializedError } from './error-class-registry.js';
import { processStackFrames, processStackString } from './error-stack.js';
import { sanitizeMessage } from './error-sanitizer.js';

export type PrimitiveTypeAnnotation = 'number' | 'undefined' | 'bigint';

type LeafTypeAnnotation = PrimitiveTypeAnnotation | 'regexp' | 'Date' | 'URL';

type TypedArrayAnnotation = ['typed-array', string];
type ClassTypeAnnotation = ['class', string];
type SymbolTypeAnnotation = ['symbol', string];
type CustomTypeAnnotation = ['custom', string];

type ErrorTypeAnnotation = 'Error' | 'Error/stack' | 'Error/frames';

type SimpleTypeAnnotation =
  | LeafTypeAnnotation
  | 'map'
  | 'set'
  | ErrorTypeAnnotation;

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

function errorClassMatches(
  error: Error,
  options: NormalizedErrorStackOptions
): boolean {
  return !options.classFilter || options.classFilter.includes(error.name);
}

function activeErrorStackMode(
  error: Error,
  superJson: SuperJSON
): 'string' | 'frames' | undefined {
  const options = superJson.errorStackOptions;
  if (!options || options.mode === 'off') return undefined;
  return errorClassMatches(error, options) ? options.mode : undefined;
}

function isAggregateError(error: Error): error is Error & { errors: any[] } {
  return (
    (typeof AggregateError === 'function' && error instanceof AggregateError) ||
    (error.name === 'AggregateError' && isArray((error as any).errors))
  );
}

function causeBudget(options: NormalizedErrorStackOptions): number {
  switch (options.includeCauses) {
    case 'direct':
      return 1;
    case 'deep':
      return options.maxCauseDepth;
    default:
      return 0;
  }
}

function serializeConfiguredError(
  error: Error,
  superJson: SuperJSON,
  options: NormalizedErrorStackOptions,
  sanitizeInherited: boolean,
  remainingCauses: number,
  visited: Set<Error>
): SerializedError {
  const matches = errorClassMatches(error, options);
  const sanitize = options.sanitizeMessage && (sanitizeInherited || matches);

  const result: SerializedError = {
    name: error.name,
    message: sanitize ? sanitizeMessage(error.message) : error.message,
  };

  const allowed = superJson.allowedErrorProps;
  allowed.forEach(prop => {
    if (prop !== 'stack' && prop !== 'stackFrames') {
      result[prop] = (error as any)[prop];
    }
  });

  if (options.mode !== 'off') {
    if (!matches) {
      if (allowed.includes('stack')) result.stack = error.stack;
    } else if (options.mode === 'string') {
      if (allowed.includes('stack') && typeof error.stack === 'string') {
        result.stack = processStackString(error.stack, options);
      }
    } else if (
      allowed.includes('stackFrames') &&
      typeof error.stack === 'string'
    ) {
      result.stackFrames = processStackFrames(error.stack, options);
    }
  }

  if (isAggregateError(error)) {
    result.errors = error.errors;
  }

  const cause = (error as any).cause;
  if (remainingCauses > 0 && isError(cause) && !visited.has(cause)) {
    visited.add(cause);
    result.cause = serializeConfiguredError(
      cause,
      superJson,
      options,
      sanitize,
      remainingCauses - 1,
      visited
    );
  }

  return result;
}

function serializeError(error: Error, superJson: SuperJSON): SerializedError {
  const options = superJson.errorStackOptions;
  let result: SerializedError;

  if (options) {
    result = serializeConfiguredError(
      error,
      superJson,
      options,
      false,
      causeBudget(options),
      new Set([error])
    );
  } else {
    result = {
      name: error.name,
      message: error.message,
    };

    if ('cause' in error) {
      result.cause = error.cause;
    }

    superJson.allowedErrorProps.forEach(prop => {
      result[prop] = (error as any)[prop];
    });
  }

  const processor =
    superJson.errorClassRegistry.getProcessor(error.name) ??
    superJson.errorClassRegistry.getProcessor(error.constructor?.name);
  return processor ? processor(result) ?? result : result;
}

function isSerializedError(v: any): v is SerializedError {
  return (
    isPlainObject(v) &&
    typeof v.name === 'string' &&
    typeof v.message === 'string'
  );
}

function deserializeError(
  v: any,
  superJson: SuperJSON,
  restoreConfigured: boolean
): Error {
  let e: any;

  if (restoreConfigured) {
    const options =
      'cause' in v
        ? {
            cause: isSerializedError(v.cause)
              ? deserializeError(v.cause, superJson, true)
              : v.cause,
          }
        : undefined;

    e =
      v.name === 'AggregateError' &&
      isArray(v.errors) &&
      typeof AggregateError === 'function'
        ? new AggregateError(v.errors, v.message, options)
        : new Error(v.message, options);

    if ('errors' in v) e.errors = v.errors;
    if ('stackFrames' in v) e.stackFrames = v.stackFrames;
  } else {
    e = new Error(v.message, { cause: v.cause });
  }

  e.name = v.name;
  e.stack = v.stack;

  superJson.allowedErrorProps.forEach(prop => {
    if (restoreConfigured && (prop === 'cause' || prop === 'errors')) return;
    e[prop] = v[prop];
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
      isError(v) && activeErrorStackMode(v, superJson) === 'string',
    'Error/stack',
    serializeError,
    (v, superJson) => deserializeError(v, superJson, true)
  ),

  simpleTransformation(
    (v, superJson): v is Error =>
      isError(v) && activeErrorStackMode(v, superJson) === 'frames',
    'Error/frames',
    serializeError,
    (v, superJson) => deserializeError(v, superJson, true)
  ),

  simpleTransformation(isError, 'Error', serializeError, (v, superJson) =>
    deserializeError(v, superJson, !!superJson.errorStackOptions)
  ),

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
