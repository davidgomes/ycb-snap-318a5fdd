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

Comment markers disable rules for part of a file. A marker is recognized only when it is the whole line, aside from leading or trailing spaces and tabs. The same instructions can be written as an HTML comment or an Obsidian comment:

- `<!-- linter-disable -->` or `%% linter-disable %%` disables every rule from the next line through the matching enable marker. If that scope is never closed, it runs through the end of the file.
- `<!-- linter-disable rule-a, rule-b -->` disables only those rule aliases.
- `<!-- linter-enable -->` closes the most recently opened disable scope.
- `<!-- linter-enable rule-a -->` stops disabling `rule-a` by removing it from the nearest open scope that currently disables it. A scope that only listed rules is closed once none remain. A scope that disables every rule stays open, with those rules turned back on inside it.
- `<!-- linter-disable-next-line -->` and `<!-- linter-disable-next-line rule-a -->` do the same thing for the next line only.
- `<!-- linter-disable-next-n-lines: 3 -->` and `<!-- linter-disable-next-n-lines: 3 rule-a, rule-b -->` do the same thing for the next 3 lines. `3` must be a positive base-10 integer. The range stops at the end of the file when there are fewer lines left. If this marker is the last line of the file, it does nothing.

`%% linter-enable %%`, `%% linter-disable-next-line %%`, and `%% linter-disable-next-n-lines: 3 %%` work the same way. Rule aliases are matched without case sensitivity. Repeated names, empty entries, and trailing commas are ignored. An unknown alias is ignored. If a list is present but every entry is unknown or empty, that marker does nothing. Leaving the list off a disable marker always disables every rule.

Scopes can be nested. Disabling every rule and then re-enabling one alias inside that region is supported.

Markers inside YAML frontmatter, fenced or indented code, inline code, or math blocks are not markers. A marker line is never edited by any rule.

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

Disable one rule, then turn that rule back on while other rules stay disabled:

``` markdown
<!-- linter-disable -->
This whole region skips every rule
<!-- linter-enable trailing-spaces -->
Trailing spaces can be cleaned up here, but other rules still skip this region
<!-- linter-enable -->
Every rule runs again here
```

!!! info
    Paste rules are not affected by ranged ignores as that would require the copied text to have a ranged ignore in it.
