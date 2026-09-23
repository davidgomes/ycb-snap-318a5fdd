# Ignoring or Disabling Rules

There are a couple of way to ignore rules in the Linter. These vary from settings in the plugin itself
to values in the YAML frontmatter, and a syntax to ignore rules for part or all of a file.

## Ignoring a Folder

There is a setting in the plugin for called `Folders to Ignore`. As the name suggests, this rule is meant
to allow users to specify folders that they do not want the linting rules to affect.
The values in the text box are expected to be folder paths from the base of the Obsidian vault.

![Setting for ignoring specific folders](../assets/folders-to-ignore.jpg)

For example, in the above image, the `templates` folder will be ignored when the Linter attempts to run its rules. Nested folders are also allowed as well.

## Ignoring Files via Regex

There is a setting in this plugin which allows you to be able to ignore files by providing a regex to match against.
If a file matches the provided regex, it will go ahead and ignore that file before it even lints the file.

![Setting for ignoring specific files via regex](../assets/files-to-ignore.jpg)

For example, in the above image you can see that Excalidraw files which end in `.exclidraw.md` are being ignored
using the regex `.*\.excalidraw\.md$`.

## File Specific Rule Disabling

There are times when there may be a need to disable a specific rule or rules for a particular file and there is no
desire to ignore all files in the folder where that file resides. In that case, there is the ability to disable a
rule or rules via the YAML frontmatter or ranged ignores.

### YAML Frontmatter

In the YAML frontmatter of a file, there is the ability to specify a list of rules to disable for the file using the key `disabled rules`.
Valid values for rules to disable are the rule aliases to disable specific rules or `all` to disable all rules for the file.

For example, the following would disable [capitalize headings](../settings/heading-rules.md#capitalize-headings) and [header increment](../settings/heading-rules.md#header-increment) for the entire file it is found in:
``` markdown
---
disabled rules: [capitalize-headings, header-increment]
---
```

The following disables all Linter rules for a file:
``` markdown
---
disabled rules: [all]
---
```

### Range Ignore

When there is a need to disable the Linter for part of a file, ranged ignores can be used. The syntax for a ranged ignore
is `<!-- linter-disable -->` or `%% linter-disable %%` with an optional `<!-- linter-enable -->` or `%% linter-enable %%` where you want the Linter to start back up with its linting.
Leaving off the ending of a range ignore will assume you want to ignore the file contents from the start of the range ignore to the end of the file. So be careful when not ending a range ignore.

A marker is only recognized when it is on its own line with nothing else on that line besides spaces or tabs.
Markers in the YAML frontmatter, code blocks, inline code, and math blocks are not recognized.
Marker lines are never modified by any rule.

!!! warning
    Ranged ignores only prevent the values in the ranged ignore from being linted. It *does not* prevent whitespace or other additions around the ranged ignore.

The following example shows how you would ignore just a part of a file:
``` markdown
Here is some text
<!-- linter-disable -->
                          This area will not be formatted
<!-- linter-enable -->
More content goes here...
%% linter-disable %%
                          This area will not be formatted
%% linter-enable %%
```

Here is another example that shows a ranged ignore without an ending indicator:
``` markdown
Here is some text
<!-- linter-disable -->
                          This area will not be formatted
This content is also not formatted either.
```

#### Disabling Specific Rules

A comma-separated list of rule aliases can be added after `linter-disable` to only disable those rules. Rule aliases are
not case-sensitive, and duplicates, empty entries, and unknown rule aliases are ignored. If no known rule alias is left in the list,
the marker has no effect.

``` markdown
<!-- linter-disable capitalize-headings, trailing-spaces -->
# this heading will not be capitalized
<!-- linter-enable -->
```

Ranged ignores can be nested. A `linter-enable` without a list of rules ends the most recently started ranged ignore that is still active.
A `linter-enable` with a list of rules re-enables each of those rules in the most recently started ranged ignore that still disables it,
and a ranged ignore for specific rules ends once all of its rules have been re-enabled. This makes it possible to disable all rules and then
re-enable just a few of them:

``` markdown
%% linter-disable %%
Nothing is linted here.
%% linter-enable trailing-spaces %%
Only trailing spaces are removed here.
%% linter-enable %%
```

#### Disabling the Next Lines

`linter-disable-next-line` disables rules for just the line after it, and `linter-disable-next-n-lines: N` disables rules for the `N` lines after it
where `N` is a positive whole number. Both can be followed by a list of rules to disable, otherwise all rules are disabled for those lines.
They have no effect when they are on the last line, and they stop at the end of the file. When the lines they cover include only part of
a code block or math block, the whole block is covered.

``` markdown
<!-- linter-disable-next-line capitalize-headings -->
# this heading will not be capitalized
%% linter-disable-next-n-lines: 2 %%
These two lines
will not be formatted.
```

!!! info
    Paste rules are not affected by ranged ignores as that would require the copied text to have a ranged ignore in it.
