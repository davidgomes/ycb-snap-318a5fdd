import { isAspect } from '../../aspect/aspect';
import type { Aspect } from '../../aspect/types';
import type { Trait } from '../../trait/types';
import type { Modifier } from '../types';
import { createModifier } from '../modifier';

export const Not = <T extends (Trait | Aspect)[] = (Trait | Aspect)[]>(
    ...inputs: T
): Modifier<Trait[], 'not'> => {
    if (inputs.length === 1 && isAspect(inputs[0])) {
        const aspect = inputs[0] as Aspect;
        const modifier = createModifier('not', 1, [...aspect.traits]) as Modifier<Trait[], 'not'>;
        modifier.aspect = aspect;
        return modifier;
    }

    return createModifier(
        'not',
        1,
        inputs.filter((input): input is Trait => !isAspect(input))
    );
};
