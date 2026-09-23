import {rulesDict} from '../src/rules';
import '../src/rules-registry';

const rule = () => rulesDict['trailing-spaces'];

describe('linter disable markers', () => {
  it('disables all rules in a scope and preserves marker lines', () => {
    const text = 'a  \n<!-- linter-disable -->  \nb  \n%% linter-enable %%\nc  \n';
    expect(rule().apply(text, {...rule().getDefaultOptions()})).toBe('a\n<!-- linter-disable -->  \nb  \n%% linter-enable %%\nc\n');
  });

  it('supports rule lists, re-enabling and next-n-lines', () => {
    const text = '<!-- linter-disable -->\na  \n<!-- linter-enable Trailing-Spaces, -->\nb  \n%% linter-disable-next-n-lines: 1 trailing-spaces %%\nc  \nd  ';
    expect(rule().apply(text, {...rule().getDefaultOptions()})).toBe('<!-- linter-disable -->\na  \n<!-- linter-enable Trailing-Spaces, -->\nb\n%% linter-disable-next-n-lines: 1 trailing-spaces %%\nc  \nd');
  });

  it('ignores markers in code blocks and invalid counts', () => {
    const text = '```\n<!-- linter-disable -->\n```\na \n<!-- linter-disable-next-n-lines: 0 -->\nb ';
    expect(rule().apply(text, {...rule().getDefaultOptions()})).toBe('```\n<!-- linter-disable -->\n```\na\n<!-- linter-disable-next-n-lines: 0 -->\nb');
  });
});
