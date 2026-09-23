import type { PropertyProps } from '~/components';

export const properties: Record<string, PropertyProps> = {
  TSchema: {
    modifier: 'extends',
    type: {
      type: 'custom',
      name: 'BaseSchema',
      href: '../BaseSchema/',
      generics: [
        'unknown',
        'unknown',
        {
          type: 'custom',
          name: 'BaseIssue',
          href: '../BaseIssue/',
          generics: ['unknown'],
        },
      ],
    },
  },
  schema: {
    type: {
      type: 'custom',
      name: 'TSchema',
    },
  },
  Schema: {
    type: {
      type: 'custom',
      name: 'RecursiveSchema',
      href: '../RecursiveSchema/',
      generics: [
        {
          type: 'custom',
          name: 'TSchema',
        },
      ],
    },
  },
};
