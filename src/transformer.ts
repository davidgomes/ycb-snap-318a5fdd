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
import { ErrorStackOptions } from './error-options.js';

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

const isAggregateError = (value: Error): value is Error & { errors: any[] } =>
  typeof AggregateError !== 'undefined' && value instanceof AggregateError;

function classMatches(error: Error, options: ErrorStackOptions): boolean {
  return !options.classFilter || options.classFilter.includes(error.name);
}

function causeForSerialization(
  cause: any,
  options: ErrorStackOptions,
  depth: number,
  seen: Set<any>
): any {
  if (!(cause instanceof Error) || seen.has(cause)) return undefined;
  if (options.includeCauses === 'deep' && depth > options.maxCauseDepth) {
    return undefined;
  }
  const copy = Object.create(Object.getPrototypeOf(cause));
  Object.defineProperties(copy, {
    name: { value: cause.name, enumerable: true, writable: true },
    message: { value: cause.message, enumerable: true, writable: true },
    stack: { value: cause.stack, enumerable: true, writable: true },
  });
  seen.add(cause);
  if (options.includeCauses === 'deep' && depth < options.maxCauseDepth) {
    if ('cause' in cause) {
      const nested = causeForSerialization(cause.cause, options, depth + 1, seen);
      if (nested) copy.cause = nested;
    }
  }
  return copy;
}

function transformError(v: Error, superJson: SuperJSON): any {
  const options = superJson.errorStackOptions;
  const matches = options ? classMatches(v, options) : false;
  const baseError: any = {
    name: v.name,
    message: matches && options?.sanitizeMessage ? sanitizeMessage(v.message) : v.message,
  };

  if (!options) {
    if ('cause' in v) baseError.cause = v.cause;
  } else {
    if (options.includeCauses !== 'none' && 'cause' in v) {
      const cause =
        options.includeCauses === 'direct'
          ? causeForSerialization(v.cause, options, options.maxCauseDepth, new Set([v]))
          : causeForSerialization(v.cause, options, 1, new Set([v]));
      if (cause) baseError.cause = cause;
    }
    if (isAggregateError(v)) baseError.errors = v.errors;

    if (
      matches &&
      options.mode === 'string' &&
      superJson.allowedErrorProps.includes('stack') &&
      typeof v.stack === 'string'
    ) {
      baseError.stack = processStackString(v.stack, options);
    }
    if (
      matches &&
      options.mode === 'frames' &&
      superJson.allowedErrorProps.includes('stackFrames') &&
      typeof v.stack === 'string'
    ) {
      baseError.stackFrames = processStackFrames(v.stack, options);
    }
  }

  superJson.allowedErrorProps.forEach(prop => {
    if (
      (!options ||
        (prop !== 'stack' && prop !== 'stackFrames' && prop !== 'cause')) &&
      !(prop in baseError)
    ) {
      baseError[prop] = (v as any)[prop];
    }
  });
  return baseError;
}

function untransformError(v: any, superJson: SuperJSON): Error {
  const isAggregate = Array.isArray(v.errors) && typeof AggregateError !== 'undefined';
  const e: any = isAggregate
    ? new AggregateError(v.errors, v.message, { cause: v.cause })
    : new Error(v.message, { cause: v.cause });
  e.name = v.name;
  e.stack = v.stack;
  if ('stackFrames' in v) e.stackFrames = v.stackFrames;
  superJson.allowedErrorProps.forEach(prop => {
    if (prop in v) e[prop] = v[prop];
  });
  return e;
}

const errorRules = [
  simpleTransformation(
    (v, superJson): v is Error => {
      if (!isError(v)) return false;
      const options = superJson.errorStackOptions;
      return !(
        options &&
        classMatches(v, options) &&
        ((options.mode === 'string' &&
          superJson.allowedErrorProps.includes('stack')) ||
          (options.mode === 'frames' &&
            superJson.allowedErrorProps.includes('stackFrames')))
      );
    },
    'Error',
    transformError,
    untransformError
  ),
  simpleTransformation(
    (v, superJson): v is Error =>
      isError(v) &&
      !!superJson.errorStackOptions &&
      superJson.errorStackOptions.mode === 'string' &&
      classMatches(v, superJson.errorStackOptions) &&
      superJson.allowedErrorProps.includes('stack'),
    'Error/stack',
    transformError,
    untransformError
  ),
  simpleTransformation(
    (v, superJson): v is Error =>
      isError(v) &&
      !!superJson.errorStackOptions &&
      superJson.errorStackOptions.mode === 'frames' &&
      classMatches(v, superJson.errorStackOptions) &&
      superJson.allowedErrorProps.includes('stackFrames'),
    'Error/frames',
    transformError,
    untransformError
  ),
];

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

  const applicableSimpleRule = findArr([...errorRules, ...simpleRules], rule =>
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

const simpleRulesByAnnotation: Record<string, any> = {};
[...errorRules, ...simpleRules].forEach(rule => {
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
