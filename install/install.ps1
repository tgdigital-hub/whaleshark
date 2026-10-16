# WhaleShark's installer for one Windows login, the twin of install.sh. It
# needs no administrator and runs nothing it fetched: it takes the program
# file and the release's signed list, checks the list's signature against
# the key below, checks the file against the list, and only then renames
# the file into place and puts its folder on the login's PATH.
#
#   irm https://github.com/tgdigital-hub/whaleshark/releases/latest/download/install.ps1 | iex
#   powershell -ExecutionPolicy Bypass -File install.ps1 -From DIR
#
# -From DIR makes the same checks on the release files in DIR. It runs in
# Windows PowerShell 5.1 and in PowerShell 7. The release fills in the two
# lines below.
param([string]$From = '')

$VERSION = 'dev'
# One allowed-signers line: release namespaces="file" <key type> <key>
$SIGNER = ''

# The rest runs in a scope of its own: under `iex` nothing it sets stays in
# the person's PowerShell, and a refusal ends this script, not the window.
& {
	$ErrorActionPreference = 'Stop'
	$ProgressPreference = 'SilentlyContinue'
	$site = 'https://github.com/tgdigital-hub/whaleshark/releases/download'
	$file = 'whaleshark-windows-amd64.exe'
	function die([string]$why) { throw "install: $why" }

	if (-not $SIGNER) { die 'this copy carries no release key: take install.ps1 from a release' }
	if (-not $From -and $VERSION -eq 'dev') { die 'this copy names no release: take install.ps1 from a release, or give -From DIR' }
	$keygen = Get-Command ssh-keygen -CommandType Application -ErrorAction SilentlyContinue | Select-Object -First 1
	if (-not $keygen) {
		die 'ssh-keygen is missing; it comes with the OpenSSH client of Windows. Add it in Settings > System > Optional features > OpenSSH Client, then open a new terminal and run this again'
	}
	$keygen = $keygen.Path
	if (-not $env:ComSpec) { die 'ComSpec is not set, so there is no cmd to hand ssh-keygen the signed list' }
	if ($env:PROCESSOR_ARCHITECTURE -eq 'x86' -and -not $env:PROCESSOR_ARCHITEW6432) { die 'no release for 32-bit Windows' }
	if (-not $env:LOCALAPPDATA) { die 'LOCALAPPDATA is not set, so there is no place for the program' }
	if ($From) {
		$dir = Resolve-Path -LiteralPath $From -ErrorAction SilentlyContinue
		if (-not $dir) { die "there is no folder $From" }
		$From = $dir.ProviderPath
	}

	# The work folder lies beside the program's place, private to this
	# login, so that the last step is a rename.
	$dest = Join-Path (Join-Path $env:LOCALAPPDATA 'Programs') 'whaleshark'
	$exe = Join-Path $dest 'whaleshark.exe'
	New-Item -ItemType Directory -Force -Path $dest | Out-Null
	if (Test-Path -LiteralPath $exe -PathType Container) { die "$exe is a folder, where the program goes; nothing was installed" }
	$work = Join-Path $dest ('.whaleshark-install.' + [IO.Path]::GetRandomFileName())
	New-Item -ItemType Directory -Path $work | Out-Null
	try {
		if (-not $From) {
			[Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12
		}
		foreach ($f in $file, 'SHA256SUMS', 'SHA256SUMS.sig') {
			$to = Join-Path $work $f
			if ($From) {
				try { Copy-Item -LiteralPath (Join-Path $From $f) -Destination $to } catch { die "$f is not in $From" }
			} else {
				try { Invoke-WebRequest -UseBasicParsing -Uri "$site/v$VERSION/$f" -OutFile $to } catch { die "could not fetch $f of version ${VERSION}: $_" }
			}
		}
		$list = Join-Path $work 'SHA256SUMS'

		# ssh-keygen reads the signed bytes on its standard input, which must
		# be the file itself. No pipe of PowerShell's or of .NET's gives that:
		# where the console's code page is UTF-8, Windows PowerShell's .NET
		# puts a byte-order mark first. cmd's < hands over the file, as sh's.
		$signers = Join-Path $work 'signers'
		[IO.File]::WriteAllText($signers, $SIGNER + "`n")
		$words = @($keygen, '-Y', 'verify', '-f', $signers, '-I', 'release', '-n', 'file', '-s', "$list.sig") | ForEach-Object { '"' + $_ + '"' }
		$p = New-Object Diagnostics.Process
		$p.StartInfo.FileName = $env:ComSpec
		$p.StartInfo.Arguments = '/d /c "' + ($words -join ' ') + ' <"' + $list + '""'
		$p.StartInfo.UseShellExecute = $false
		$p.StartInfo.RedirectStandardOutput = $true
		$p.StartInfo.RedirectStandardError = $true
		[void]$p.Start()
		$said = $p.StandardOutput.ReadToEndAsync(), $p.StandardError.ReadToEndAsync()
		$p.WaitForExit()
		if ($p.ExitCode -ne 0) {
			if (($said[0].Result + $said[1].Result) -match 'unknown option|illegal option|usage:') {
				die 'this ssh-keygen is too old to check a signature: OpenSSH 8.1 or newer is needed (Windows Update brings it to Windows 10 and 11); nothing was installed'
			}
			die 'the list of checksums is not signed by the release key; nothing was installed'
		}
		# An older release, rightly signed, must not pass for this one.
		$lines = [IO.File]::ReadAllText($list) -split "`n"
		if ($VERSION -ne 'dev' -and $lines -cnotcontains "# whaleshark $VERSION") { die "the signed list is not version ${VERSION}'s; nothing was installed" }

		$want = @(foreach ($l in $lines) { $w = $l -split '\s+'; if ($w.Count -ge 2 -and ($w[1] -ceq $file -or $w[1] -ceq "*$file")) { $w[0] } })
		if (-not $want) { die "the signed list has no line for $file" }
		$got = (Get-FileHash -Algorithm SHA256 -LiteralPath (Join-Path $work $file)).Hash
		if ($got -ne $want[0]) { die "$file is not the file the release signed; nothing was installed" }

		# A browser's download carries a mark that makes Windows warn before
		# it runs; a file checked as above needs none.
		if (Get-Command Unblock-File -ErrorAction SilentlyContinue) { Unblock-File -LiteralPath (Join-Path $work $file) }
		# Windows lets the file of a running program be renamed, not replaced.
		if (Test-Path -LiteralPath $exe) {
			Remove-Item -LiteralPath "$exe.old" -Force -ErrorAction SilentlyContinue
			try { Move-Item -LiteralPath $exe -Destination "$exe.old" } catch { die "could not move the old $exe aside: close every whaleshark and try again" }
		}
		Move-Item -LiteralPath (Join-Path $work $file) -Destination $exe
		Remove-Item -LiteralPath "$exe.old" -Force -ErrorAction SilentlyContinue
		Write-Host "installed $exe"

		# The login's own PATH, read and written as it is kept, so that a
		# part written with %NAME% in it stays so.
		if ([Environment]::OSVersion.Platform -eq 'Win32NT') {
			$path = [string](Get-Item -LiteralPath 'HKCU:\Environment').GetValue('Path', '', 'DoNotExpandEnvironmentNames')
			if (-not ($path -split ';' | Where-Object { $_.TrimEnd('\') -eq $dest })) {
				Set-ItemProperty -LiteralPath 'HKCU:\Environment' -Name Path -Type ExpandString -Value ((@($path.TrimEnd(';'), $dest) | Where-Object { $_ }) -join ';')
				# Setting a variable this way tells Windows that the
				# login's variables changed, so new terminals see the PATH.
				[Environment]::SetEnvironmentVariable('WHALESHARK_INSTALL', '1', 'User')
				[Environment]::SetEnvironmentVariable('WHALESHARK_INSTALL', $null, 'User')
				Write-Host "added $dest to your PATH"
			}
		}

		Write-Host 'The release key, to compare with the README:'
		$pub = Join-Path $work 'key.pub'
		[IO.File]::WriteAllText($pub, ($SIGNER -replace '^release namespaces="file" ', '') + "`n")
		& $keygen -l -f $pub | Write-Host
		Write-Host 'Next: open a new terminal and type whaleshark in a project.'
	} finally {
		Remove-Item -LiteralPath $work -Recurse -Force -ErrorAction SilentlyContinue
	}
}
