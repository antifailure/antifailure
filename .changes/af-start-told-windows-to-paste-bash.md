# fixed

`af start` told a Windows user to paste bash. When af was installed and not yet
on the terminal's PATH, it offered `export PATH=...`, and when another af came
first on PATH it offered `which -a af`; neither exists in PowerShell, which is
where install.ps1 leaves the person. It also looked for the install under the
name `af`, so on Windows, where the file is `af.exe`, it never found it and
called an installed af a development build. It now looks for `af.exe` and
offers `$env:Path = "...;" + $env:Path` and `where.exe af` on Windows.
