import type { PropertyProps } from '~/components';

export const properties: Record<string, PropertyProps> = {
  BaseSchema: {
    modifier: 'extends',
    type: {
      type: 'custom',
      name: 'BaseSchema',
      href: '../BaseSchema/',
      generics: [
        {
          type: 'custom',
          name: 'RecurInput',
          href: '../RecurInput/',
        },
        {
          type: 'custom',
          name: 'RecurOutput',
          href: '../RecurOutput/',
        },
        {
          type: 'custom',
          name: 'RecurIssue',
          href: '../RecurIssue/',
        },
      ],
    },
  },
  type: {
    type: {
      type: 'string',
      value: 'recur',
    },
  },
  reference: {
    type: {
      type: 'function',
      params: [],
      return: {
        type: 'custom',
        name: 'RecurSchema',
        href: '../RecurSchema/',
      },
    },
  },
  expects: {
    type: {
      type: 'string',
      value: 'Recur',
    },
  },
};
