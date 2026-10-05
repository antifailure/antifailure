# Antifailure installer for Windows.
#
#   irm https://antifailure.dev/install.ps1 | iex
#
# Downloads the release for this machine, checks it against the published
# checksum, and puts af.exe and the runner where af expects them. It is the
# Windows twin of install.sh and makes the same promises, in the same order:
# a version is resolved without the rate limited API, nothing unverified is
# ever installed, every failure says what the server actually answered, and the
# install ends with af on the PATH rather than with instructions for putting it
# there.
#
# Written for Windows PowerShell 5.1 as well as PowerShell 7, because 5.1 is
# the one every Windows machine already has, and a script that needs 7 on a
# machine without it fails with a parse error rather than a message. That rules
# out the null coalescing operator, the ternary and Invoke-WebRequest's newer
# switches, and it is why the HTTP below goes through HttpClient directly: it
# behaves the same in both, and it can be told not to follow a redirect, which
# is the one thing the version lookup needs.
#
# Everything runs inside one script block. `irm | iex` evaluates this text in
# the caller's own session, so a variable or preference set at the top level
# here would still be set in their terminal afterwards. Inside the block they
# are local, and the session is left as it was found, apart from PATH, which
# is the point.
#
# Settings, all optional:
#
#   AF_VERSION         a release tag to install, rather than the newest
#   AF_PREFIX          where to install, default %USERPROFILE%\.antifailure
#   AF_BIN_DIR         where af.exe goes, default <AF_PREFIX>\bin
#   AF_NO_MODIFY_PATH  set to anything to leave the user PATH alone
#   AF_GITHUB          the address of GitHub, for a mirror of it; the default
#                      is https://github.com

& {
  Set-StrictMode -Version 2
  $ErrorActionPreference = 'Stop'
  $ProgressPreference = 'SilentlyContinue'

  $Repo = 'antifailure/antifailure'
  $GitHub = 'https://github.com'
  if ($env:AF_GITHUB) { $GitHub = $env:AF_GITHUB.TrimEnd('/') }

  $Version = 'latest'
  if ($env:AF_VERSION) { $Version = $env:AF_VERSION }

  $UserHome = $env:USERPROFILE
  if (-not $UserHome) { $UserHome = $HOME }
  $Prefix = Join-Path $UserHome '.antifailure'
  if ($env:AF_PREFIX) { $Prefix = $env:AF_PREFIX }
  $BinDir = Join-Path $Prefix 'bin'
  if ($env:AF_BIN_DIR) { $BinDir = $env:AF_BIN_DIR }

  # A failure prints its own sentence and then stops the script with a short
  # terminating error. Both halves are needed. `exit` would close the terminal
  # of somebody who pasted the one line command, and returning quietly would
  # leave `powershell -Command "irm ... | iex"` in a CI job exiting 0 having
  # installed nothing. A terminating error is the one thing that keeps the
  # terminal open and still fails the process that ran it.
  function Fail([string]$Message) {
    $Host.UI.WriteErrorLine("antifailure: $Message")
    throw 'Antifailure was not installed. The reason is above.'
  }

  function Say([string]$Message) { Write-Host $Message }

  if ([Environment]::OSVersion.Platform -ne 'Win32NT') {
    Fail "this is the Windows installer. On macOS and Linux install with: curl -fsSL https://antifailure.dev/install.sh | sh"
  }

  # OSArchitecture rather than PROCESSOR_ARCHITECTURE, because the second one
  # describes the process and not the machine: an x64 PowerShell running under
  # emulation on a Windows on Arm laptop says AMD64, and would install the
  # emulated build on a machine that can run the native one. The environment
  # variables are the fallback for a .NET too old to have the property.
  $arch = $null
  try { $arch = [System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture.ToString() } catch { $arch = $null }
  if (-not $arch) {
    $arch = $env:PROCESSOR_ARCHITEW6432
    if (-not $arch) { $arch = $env:PROCESSOR_ARCHITECTURE }
  }
  switch ($arch) {
    'X64'   { $arch = 'amd64' }
    'AMD64' { $arch = 'amd64' }
    'Arm64' { $arch = 'arm64' }
    default { Fail "$arch is not an architecture this release supports" }
  }

  # TLS 1.2 added, never anything removed. Windows PowerShell 5.1 on an older
  # .NET offers only TLS 1.0 and 1.1 by default, and github.com refuses both,
  # which surfaces as a connection that "could not be created" with no mention
  # of TLS at all. The setting is process wide, so it is restored on the way out.
  $savedProtocols = [Net.ServicePointManager]::SecurityProtocol
  [Net.ServicePointManager]::SecurityProtocol = $savedProtocols -bor [Net.SecurityProtocolType]::Tls12

  Add-Type -AssemblyName System.Net.Http
  Add-Type -AssemblyName System.IO.Compression.FileSystem

  $agent = 'antifailure-install-ps1'

  $followHandler = New-Object System.Net.Http.HttpClientHandler
  $followHandler.AllowAutoRedirect = $true
  $client = New-Object System.Net.Http.HttpClient($followHandler)
  $client.Timeout = [TimeSpan]::FromMinutes(10)
  $client.DefaultRequestHeaders.UserAgent.ParseAdd($agent)

  # The probe asks what a URL answers WITHOUT following a redirect, because a
  # redirect is the answer to "which release is the newest", and it is bounded
  # at thirty seconds because it runs to explain a failure and an explanation
  # that hangs is worse than the failure.
  $probeHandler = New-Object System.Net.Http.HttpClientHandler
  $probeHandler.AllowAutoRedirect = $false
  $probeClient = New-Object System.Net.Http.HttpClient($probeHandler)
  $probeClient.Timeout = [TimeSpan]::FromSeconds(30)
  $probeClient.DefaultRequestHeaders.UserAgent.ParseAdd($agent)

  $headersOnly = [System.Net.Http.HttpCompletionOption]::ResponseHeadersRead

  # Fetch reports only whether the file arrived. Why it did not is asked
  # separately, by WhyNot, on the way to an error, so nothing on the working
  # path pays for the extra request.
  function Fetch([string]$Url, [string]$Path) {
    try {
      $response = $client.GetAsync($Url, $headersOnly).GetAwaiter().GetResult()
      try {
        if (-not $response.IsSuccessStatusCode) { return $false }
        $file = [System.IO.File]::Create($Path)
        try { $response.Content.CopyToAsync($file).GetAwaiter().GetResult() } finally { $file.Dispose() }
        return $true
      } finally { $response.Dispose() }
    } catch {
      return $false
    }
  }

  # Status 0 is "nothing answered at all", which is a different fact from any
  # status a server sends, and every message below depends on which one it is.
  function Probe([string]$Url) {
    try {
      $response = $probeClient.GetAsync($Url, $headersOnly).GetAwaiter().GetResult()
      try {
        $location = ''
        if ($response.Headers.Location) {
          $location = $response.Headers.Location
          if (-not $location.IsAbsoluteUri) { $location = New-Object Uri((New-Object Uri $Url), $location) }
          $location = $location.ToString()
        }
        return @{ Status = [int]$response.StatusCode; Location = $location }
      } finally { $response.Dispose() }
    } catch {
      return @{ Status = 0; Location = '' }
    }
  }

  # WhyNot says what the server answered rather than the conclusion it is
  # tempting to jump to. "Could not be downloaded" is true of a 404 and false
  # of a timeout, a proxy and a rate limit, and only some of those are worth
  # running again.
  function WhyNot([string]$Url, [string]$What, [string]$Then) {
    $answer = Probe $Url
    $status = $answer.Status
    if ($status -eq 0) {
      return "nothing answered at $Url, so $What did not arrive and $Then. Check that this machine can reach github.com, then run this again"
    }
    if ($status -eq 403 -or $status -eq 429) {
      return "github.com answered $status for $Url, which is what it tells an address that has asked for too much, so $What did not arrive and $Then. Wait and run this again"
    }
    if ($status -eq 404) {
      # A 404 on an asset has two causes that send the reader in opposite
      # directions: there is no such release, or the release has no build for
      # this machine. The release page says which.
      $tagUrl = "$GitHub/$Repo/releases/tag/$Version"
      $tag = (Probe $tagUrl).Status
      if ($tag -eq 404) {
        return "there is no release ${Version}: github.com answered 404 for $Url and for $tagUrl, and $Then. The releases that do exist are listed at $GitHub/$Repo/releases"
      }
      if ($tag -ge 200 -and $tag -lt 400) {
        return "github.com answered 404 for $Url, so release $Version does not include $What and $Then. What it does include is listed at $tagUrl"
      }
      return "github.com answered 404 for $Url, so $What did not arrive and $Then"
    }
    return "github.com answered $status for $Url, so $What did not arrive and $Then"
  }

  $tmp = Join-Path ([System.IO.Path]::GetTempPath()) ('af-install-' + [guid]::NewGuid().ToString('N'))
  New-Item -ItemType Directory -Path $tmp -Force | Out-Null

  try {
    # Resolving "latest" without asking the API, which allows sixty
    # unauthenticated requests an hour per address and is spent by anybody
    # behind a shared NAT. github.com/<repo>/releases/latest answers the same
    # question with a redirect to releases/tag/<tag> and carries no budget.
    # install.sh explains the history in full; this is the same lookup.
    if ($Version -eq 'latest') {
      $latestUrl = "$GitHub/$Repo/releases/latest"
      $answer = Probe $latestUrl
      $status = $answer.Status
      $location = $answer.Location
      $Version = ''
      $refused = $false
      $landed = ''

      # The redirect is the one part of this exchange the far end chooses, so
      # it is treated as input: anything outside the characters a tag is made
      # of is no answer, because it is only ever used to compose URLs here.
      if ($location -match '/releases/tag/([^/]+)$') {
        $tag = $Matches[1]
        if ($tag -match '^[0-9A-Za-z._+-]+$') { $Version = $tag } else { $refused = $true }
      } elseif ($location -match '/releases/?$') {
        $landed = 'index'
      } elseif ($location) {
        $landed = 'elsewhere'
      }

      # A second way to ask, for an answer that carried no redirect to read:
      # the newest release's own checksums.txt, which names every archive with
      # its version in the file name.
      if (-not $Version -and -not $refused -and -not $landed -and $status -ge 200 -and $status -lt 400) {
        $latestSums = Join-Path $tmp 'latest-checksums.txt'
        if (Fetch "$GitHub/$Repo/releases/latest/download/checksums.txt" $latestSums) {
          foreach ($line in [System.IO.File]::ReadAllLines($latestSums)) {
            if ($line -match '^[0-9a-f]{64}\s+antifailure_([0-9][0-9A-Za-z.+-]*)_[a-z0-9]+_[a-z0-9]+\.(tar\.gz|zip)$') {
              $Version = 'v' + $Matches[1]
              break
            }
          }
        }
      }

      if (-not $Version) {
        $pick = "Set AF_VERSION to a tag from $GitHub/$Repo/releases to install a specific release"
        # What was refused is not quoted back: it is somebody else's bytes, and
        # a terminal prints whatever control characters they hold.
        if ($refused) {
          Fail "$latestUrl pointed at something that is not a release tag, so which release is the newest could not be established. $pick"
        }
        if ($status -eq 0) {
          Fail "nothing answered at $latestUrl, so which release is the newest could not be established. Check that this machine can reach github.com, then run this again. $pick"
        }
        if ($status -eq 403 -or $status -eq 429) {
          Fail "github.com answered $status for $latestUrl, which is what it tells an address that has asked for too much, so which release is the newest could not be established. Wait and run this again. $pick"
        }
        if ($status -eq 404) {
          Fail "github.com answered 404 for $latestUrl, so there is no $Repo to install from, or it is not public"
        }
        if ($landed -eq 'index') {
          Fail "github.com answered $status for $latestUrl and pointed at the list of releases rather than at one, so $Repo has published no release to install. $pick"
        }
        if ($landed -eq 'elsewhere') {
          Fail "$latestUrl was answered with $status and a redirect to somewhere that is not a release, which is what a proxy or a sign-in portal in front of this network answers with, so which release is the newest could not be established. $pick"
        }
        Fail "github.com answered $status for $latestUrl and named no release, so which release is the newest could not be established. $pick"
      }
    }

    $bare = $Version
    if ($bare.StartsWith('v')) { $bare = $bare.Substring(1) }
    $name = "antifailure_${bare}_windows_$arch"
    $archive = "$name.zip"
    $base = "$GitHub/$Repo/releases/download/$Version"
    $zipPath = Join-Path $tmp $archive

    Say "Downloading $name"
    if (-not (Fetch "$base/$archive" $zipPath)) {
      Fail (WhyNot "$base/$archive" "the build for windows $arch" 'nothing was installed')
    }

    # The checksum is checked rather than assumed, and there is no path
    # through this block that installs an unverified archive. A missing
    # checksums.txt, a checksums.txt with no line for this archive, and a hash
    # that does not match all stop the install, because each of them is the
    # case somebody tampering with a download arranges.
    $sumsPath = Join-Path $tmp 'checksums.txt'
    if (-not (Fetch "$base/checksums.txt" $sumsPath)) {
      Fail (WhyNot "$base/checksums.txt" 'checksums.txt' 'the download cannot be verified, so this refuses to install')
    }

    $expected = ''
    foreach ($line in [System.IO.File]::ReadAllLines($sumsPath)) {
      if ($line -match '^([0-9a-fA-F]{64})\s+\*?(\S+)\s*$' -and $Matches[2] -eq $archive) {
        $expected = $Matches[1].ToLowerInvariant()
        break
      }
    }
    if (-not $expected) {
      Fail "checksums.txt for $Version names no $archive, so the download cannot be verified; refusing to install"
    }

    # .NET's own SHA256 rather than Get-FileHash, which lives in a module a
    # locked down machine can be without. The class is part of the runtime.
    $sha = [System.Security.Cryptography.SHA256]::Create()
    $stream = [System.IO.File]::OpenRead($zipPath)
    try { $digest = $sha.ComputeHash($stream) } finally { $stream.Dispose(); $sha.Dispose() }
    $actual = -join ($digest | ForEach-Object { $_.ToString('x2') })
    if ($actual -ne $expected) {
      Fail 'the download does not match its published checksum; refusing to install'
    }
    Say 'Checksum verified'

    # Unpacked by the runtime's ZipFile, which refuses an entry whose path
    # would land outside the destination, so a crafted archive cannot write
    # anywhere but here even before anything is checked.
    $unpacked = Join-Path $tmp 'unpacked'
    try {
      [System.IO.Compression.ZipFile]::ExtractToDirectory($zipPath, $unpacked)
    } catch {
      Fail "$archive could not be unpacked, although it matched its published checksum; the release archive is damaged, so please report it at https://github.com/$Repo/issues"
    }
    $tree = Join-Path $unpacked $name

    # A hash proves the bytes arrived intact. It says nothing about the release
    # having been assembled with every file in it, which is a build time
    # mistake the hash cannot see, so the files the install needs are checked.
    foreach ($want in @('af.exe', 'runner\src\main.ts', 'runner\package.json')) {
      if (-not (Test-Path -LiteralPath (Join-Path $tree $want))) {
        Fail "$archive unpacked with no $($want.Replace('\', '/')) in it, so this release is incomplete; refusing to install, and please report it at https://github.com/$Repo/issues"
      }
    }
    $unpinned = -not (Test-Path -LiteralPath (Join-Path $tree 'runner\package-lock.json'))

    foreach ($dir in @($BinDir, $Prefix)) {
      try {
        New-Item -ItemType Directory -Path $dir -Force | Out-Null
      } catch {
        Fail "$dir could not be created, so nothing was installed; check that you can write to it, or set AF_PREFIX or AF_BIN_DIR to somewhere you can"
      }
    }

    # Windows will not let a running .exe be overwritten or deleted, but it
    # will let one be renamed. So an af.exe already in place is moved aside
    # first, which is what makes reinstalling over an af that is serving the
    # MCP server in an editor work at all, and the new one is copied into the
    # name it left. af.exe.old is what af update leaves too, and the next run
    # of af removes it.
    $target = Join-Path $BinDir 'af.exe'
    $aside = $null
    if (Test-Path -LiteralPath $target) {
      $aside = "$target.old"
      if (Test-Path -LiteralPath $aside) {
        try { Remove-Item -LiteralPath $aside -Force } catch { $aside = "$target.$([guid]::NewGuid().ToString('N')).old" }
      }
      try {
        Move-Item -LiteralPath $target -Destination $aside -Force
      } catch {
        Fail "the af.exe already in $BinDir could not be moved aside to make room for this one, so nothing was installed; close anything running it and run this again"
      }
    }
    try {
      Copy-Item -LiteralPath (Join-Path $tree 'af.exe') -Destination $target -Force
    } catch {
      if ($aside) { try { Move-Item -LiteralPath $aside -Destination $target -Force } catch { } }
      Fail "af.exe could not be written to $BinDir; check that you can write to it, or set AF_PREFIX to somewhere you can"
    }

    # The runner source lands where `af runner install` looks for one, which is
    # share\antifailure\runner beside the bin directory. A tree an earlier
    # installer left at <prefix>\runner with no dependencies would be found
    # first and fail inside af test, so it is removed.
    $share = Join-Path $Prefix 'share\antifailure'
    $runner = Join-Path $share 'runner'
    try {
      if (Test-Path -LiteralPath $runner) { Remove-Item -LiteralPath $runner -Recurse -Force }
      New-Item -ItemType Directory -Path $share -Force | Out-Null
      Copy-Item -LiteralPath (Join-Path $tree 'runner') -Destination $runner -Recurse -Force
    } catch {
      Fail "the runner could not be written to $share; check that you can write to it, or set AF_PREFIX to somewhere you can"
    }
    $stale = Join-Path $Prefix 'runner'
    if ((Test-Path -LiteralPath $stale) -and -not (Test-Path -LiteralPath (Join-Path $stale 'node_modules'))) {
      Remove-Item -LiteralPath $stale -Recurse -Force -ErrorAction SilentlyContinue
    }

    Say ''
    Say "Installed $Version to $target"
    if ($unpinned) {
      Say ''
      Say "warning: $Version shipped no runner/package-lock.json, so af runner install"
      Say 'will resolve the runner''s dependency ranges as they are today rather than'
      Say 'installing what this release was tested with. af runner check reports it.'
    }

    # Putting af where the next terminal will find it.
    #
    # The user PATH lives in the registry, and it is read RAW, with its
    # %VARIABLES% unexpanded, and written back as an expandable string.
    # [Environment]::GetEnvironmentVariable('Path', 'User') returns it
    # expanded, and writing that back would freeze every %VARIABLE% another
    # program had put there into whatever it happened to expand to today.
    function SamePath([string]$a, [string]$b) {
      return [string]::Equals($a.TrimEnd('\'), $b.TrimEnd('\'), [StringComparison]::OrdinalIgnoreCase)
    }
    function OnPath([string]$PathValue) {
      foreach ($entry in ($PathValue -split ';')) {
        if (-not $entry) { continue }
        if ((SamePath $entry $BinDir) -or (SamePath ([Environment]::ExpandEnvironmentVariables($entry)) $BinDir)) { return $true }
      }
      return $false
    }

    # Written as %USERPROFILE%\... when it is under the profile, for the same
    # reason install.sh writes $HOME: a profile that moves does not leave a
    # dead entry behind.
    $pathRef = $BinDir
    if ($UserHome -and $BinDir.StartsWith($UserHome + '\', [StringComparison]::OrdinalIgnoreCase)) {
      $pathRef = '%USERPROFILE%' + $BinDir.Substring($UserHome.Length)
    }

    $reason = ''
    if ($env:GITHUB_PATH) {
      $reason = 'ci'
    } elseif ($env:AF_NO_MODIFY_PATH) {
      $reason = 'declined'
    } else {
      $key = $null
      try {
        $key = [Microsoft.Win32.Registry]::CurrentUser.CreateSubKey('Environment')
        $raw = [string]$key.GetValue('Path', '', [Microsoft.Win32.RegistryValueOptions]::DoNotExpandEnvironmentNames)
        if (OnPath $raw) {
          $reason = 'already'
        } else {
          $value = $pathRef
          if ($raw) { $value = $raw.TrimEnd(';') + ';' + $pathRef }
          $key.SetValue('Path', $value, [Microsoft.Win32.RegistryValueKind]::ExpandString)
          $reason = 'wrote'
        }
      } catch {
        $reason = 'write_failed'
      } finally {
        if ($key) { $key.Dispose() }
      }
      if ($reason -eq 'wrote') {
        # A registry write tells nobody. Setting a user variable through
        # .NET broadcasts the change, which is what makes a terminal opened
        # from Explorer or a fresh Windows Terminal tab see the new PATH
        # without signing out. The variable is set and removed again; the
        # broadcast is the only thing it is for.
        [Environment]::SetEnvironmentVariable('AF_INSTALL_PATH_REFRESH', '1', 'User')
        [Environment]::SetEnvironmentVariable('AF_INSTALL_PATH_REFRESH', $null, 'User')
      }
    }

    # This session too, so `af start` works in the terminal that ran the
    # one line install. Under `irm | iex` this is the reader's own session;
    # under `powershell -File` it is a child that ends with the script, which
    # is why the message below does not promise it.
    if ($reason -ne 'declined' -and $reason -ne 'write_failed' -and -not (OnPath $env:Path)) {
      $env:Path = $BinDir + ';' + $env:Path
    }

    Say ''
    switch ($reason) {
      'ci' {
        # Each step of a job gets a fresh process, so the step that runs af is
        # never the one that installed it. GITHUB_PATH is what the runner
        # reads between steps.
        $present = $false
        if (Test-Path -LiteralPath $env:GITHUB_PATH) {
          foreach ($line in [System.IO.File]::ReadAllLines($env:GITHUB_PATH)) { if (SamePath $line $BinDir) { $present = $true } }
        }
        # AppendAllText rather than Add-Content, because Add-Content in
        # Windows PowerShell 5.1 writes a byte order mark into an empty file,
        # and the runner would then read the first PATH entry with three bytes
        # in front of it.
        if (-not $present) { [System.IO.File]::AppendAllText($env:GITHUB_PATH, $BinDir + [Environment]::NewLine) }
        Say "Added $BinDir to PATH for the rest of this job."
        Say ''
        Say 'Next:'
        Say '  af start           where you are, and what to run next'
      }
      'wrote' {
        Say "Added $pathRef to your user PATH, so every new terminal finds af."
        Say 'Remove it in Settings, under Edit environment variables for your account,'
        Say 'or install with AF_NO_MODIFY_PATH set to skip this step.'
        Say ''
        Say 'Next:'
        Say '  af start           where you are, and what to run next'
      }
      'already' {
        Say 'Next:'
        Say '  af start           where you are, and what to run next'
      }
      default {
        if ($reason -eq 'declined') {
          Say 'AF_NO_MODIFY_PATH is set, so your user PATH was left alone and af is not on it.'
        } else {
          Say 'Your user PATH could not be written, so af is not on it.'
        }
        Say ''
        Say '1. Add this directory to your user PATH, in Settings, under Edit environment'
        Say '   variables for your account:'
        Say ''
        Say "     $BinDir"
        Say ''
        Say '2. Until then af answers to its full path. This says where you are on the'
        Say '   first run and what to run next, every time you run it:'
        Say ''
        Say "     & '$target' start"
      }
    }

    # Said by the installer because the installer knows now and the reader is
    # reading now.
    if (-not (Get-Command node -ErrorAction SilentlyContinue)) {
      Say ''
      Say 'node was not found, and af runner install needs node 22.6 or newer.'
      Say 'Get it from https://nodejs.org, or with: winget install OpenJS.NodeJS.LTS'
    }
  } finally {
    Remove-Item -LiteralPath $tmp -Recurse -Force -ErrorAction SilentlyContinue
    [Net.ServicePointManager]::SecurityProtocol = $savedProtocols
    $client.Dispose()
    $probeClient.Dispose()
  }
}
