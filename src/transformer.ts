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
import { matchesErrorClassFilter } from './error-options.js';
import { sanitizeMessage } from './error-sanitizer.js';
import {
  processStackFrames,
  processStackString,
} from './error-stack.js';

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

const causeDepthByInstance = new WeakMap<object, WeakMap<object, number>>();

export function resetErrorCauseDepths(superJson: object): void {
  causeDepthByInstance.set(superJson, new WeakMap());
}

function causeDepths(superJson: object): WeakMap<object, number> {
  let depths = causeDepthByInstance.get(superJson);
  if (!depths) {
    depths = new WeakMap();
    causeDepthByInstance.set(superJson, depths);
  }
  return depths;
}

function errorMessage(v: Error, superJson: SuperJSON): string {
  const options = superJson.errorStack;
  if (
    !options?.sanitizeMessage ||
    !matchesErrorClassFilter(v.name, options.classFilter)
  ) {
    return v.message;
  }
  return typeof v.message === 'string' ? sanitizeMessage(v.message) : v.message;
}

function selectCause(v: Error, superJson: SuperJSON): unknown {
  const options = superJson.errorStack;
  if (!options) {
    return 'cause' in v ? (v as { cause?: unknown }).cause : undefined;
  }

  if (options.includeCauses === 'none') {
    return undefined;
  }

  const cause = (v as { cause?: unknown }).cause;
  if (!(cause instanceof Error)) {
    return undefined;
  }

  const depth = causeDepths(superJson).get(v) ?? 0;
  if (options.includeCauses === 'direct') {
    if (depth !== 0) {
      return undefined;
    }
  } else if (depth >= options.maxCauseDepth) {
    return undefined;
  }

  causeDepths(superJson).set(cause, depth + 1);
  return cause;
}

function serializeError(
  v: Error,
  superJson: SuperJSON,
  kind: 'default' | 'stack' | 'frames'
): Record<string, any> {
  const options = superJson.errorStack;

  if (!options) {
    const baseError: Record<string, any> = {
      name: v.name,
      message: v.message,
    };

    if ('cause' in v) {
      baseError.cause = (v as { cause?: unknown }).cause;
    }

    superJson.allowedErrorProps.forEach(prop => {
      baseError[prop] = (v as any)[prop];
    });

    return baseError;
  }

  const baseError: Record<string, any> = {
    name: v.name,
    message: errorMessage(v, superJson),
  };

  const cause = selectCause(v, superJson);
  if (cause !== undefined) {
    baseError.cause = cause;
  }

  if (typeof AggregateError !== 'undefined' && v instanceof AggregateError) {
    baseError.errors = v.errors;
  }

  superJson.allowedErrorProps.forEach(prop => {
    if (prop === 'cause') {
      return;
    }
    if (
      options.mode === 'off' &&
      (prop === 'stack' || prop === 'stackFrames')
    ) {
      return;
    }
    if (kind === 'stack' && prop === 'stack') {
      return;
    }
    if (kind === 'frames' && prop === 'stackFrames') {
      return;
    }
    baseError[prop] = (v as any)[prop];
  });

  if (
    kind === 'stack' &&
    typeof v.stack === 'string' &&
    superJson.allowedErrorProps.indexOf('stack') !== -1
  ) {
    baseError.stack = processStackString(v.stack, options);
  }

  if (
    kind === 'frames' &&
    typeof v.stack === 'string' &&
    superJson.allowedErrorProps.indexOf('stackFrames') !== -1
  ) {
    baseError.stackFrames = processStackFrames(v.stack, options);
  }

  return baseError;
}

function deserializeError(v: any, superJson: SuperJSON): Error {
  let e: Error;
  if (
    typeof AggregateError !== 'undefined' &&
    v?.name === 'AggregateError' &&
    Array.isArray(v.errors)
  ) {
    e = new AggregateError(v.errors, v.message, { cause: v.cause });
  } else {
    e = new Error(v.message, { cause: v.cause });
  }

  e.name = v.name;
  e.stack = v.stack;

  if (v.stackFrames !== undefined) {
    (e as any).stackFrames = v.stackFrames;
  }
  if (v.name !== 'AggregateError' && v.errors !== undefined) {
    (e as any).errors = v.errors;
  }

  superJson.allowedErrorProps.forEach(prop => {
    (e as any)[prop] = v[prop];
  });

  return e;
}

function matchesStackMode(
  v: any,
  superJson: SuperJSON,
  mode: 'string' | 'frames'
): v is Error {
  const options = superJson.errorStack;
  return (
    isError(v) &&
    !!options &&
    options.mode === mode &&
    matchesErrorClassFilter(v.name, options.classFilter)
  );
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
    (v, superJson): v is Error => matchesStackMode(v, superJson, 'string'),
    'Error/stack',
    (v, superJson) => serializeError(v, superJson, 'stack'),
    deserializeError
  ),

  simpleTransformation(
    (v, superJson): v is Error => matchesStackMode(v, superJson, 'frames'),
    'Error/frames',
    (v, superJson) => serializeError(v, superJson, 'frames'),
    deserializeError
  ),

  simpleTransformation(
    isError,
    'Error',
    (v, superJson) => serializeError(v, superJson, 'default'),
    deserializeError
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
