# Renders the PWA icon set into public/ from the same design as favicon.svg.
# Run from apps/web: pwsh -File scripts/generate-icons.ps1
Add-Type -AssemblyName System.Drawing

$publicDir = Join-Path $PSScriptRoot '..\public'

function New-RoundedRectPath([float]$x, [float]$y, [float]$w, [float]$h, [float]$r) {
  $path = New-Object System.Drawing.Drawing2D.GraphicsPath
  $d = $r * 2
  $path.AddArc($x, $y, $d, $d, 180, 90)
  $path.AddArc($x + $w - $d, $y, $d, $d, 270, 90)
  $path.AddArc($x + $w - $d, $y + $h - $d, $d, $d, 0, 90)
  $path.AddArc($x, $y + $h - $d, $d, $d, 90, 90)
  $path.CloseFigure()
  return $path
}

function New-Icon([int]$size, [string]$outName, [bool]$maskable) {
  $bmp = New-Object System.Drawing.Bitmap($size, $size)
  $g = [System.Drawing.Graphics]::FromImage($bmp)
  $g.SmoothingMode = [System.Drawing.Drawing2D.SmoothingMode]::AntiAlias

  $bg = [System.Drawing.Color]::FromArgb(255, 15, 15, 15)
  $green = [System.Drawing.Color]::FromArgb(255, 34, 197, 94)
  $bgBrush = New-Object System.Drawing.SolidBrush($bg)
  $greenBrush = New-Object System.Drawing.SolidBrush($green)

  $s = $size / 512.0

  if ($maskable) {
    # Full-bleed background; safe zone keeps art within the inner 80%.
    $g.FillRectangle($bgBrush, 0, 0, $size, $size)
    $glyphScale = 0.82
  } else {
    $g.Clear([System.Drawing.Color]::Transparent)
    $corner = New-RoundedRectPath 0 0 $size $size (96 * $s)
    $g.FillPath($bgBrush, $corner)
    $corner.Dispose()
    $glyphScale = 1.0
  }

  # Scale the glyph around the canvas center.
  $g.TranslateTransform($size / 2.0, $size / 2.0)
  $g.ScaleTransform($glyphScale, $glyphScale)
  $g.TranslateTransform(-$size / 2.0, -$size / 2.0)

  $accent = New-RoundedRectPath (80 * $s) (80 * $s) (352 * $s) (352 * $s) (80 * $s)
  $g.FillPath($greenBrush, $accent)
  $accent.Dispose()

  foreach ($barY in @(176, 272)) {
    $bar = New-RoundedRectPath (156 * $s) ($barY * $s) (200 * $s) (64 * $s) (20 * $s)
    $g.FillPath($bgBrush, $bar)
    $bar.Dispose()
    $cy = ($barY + 32) * $s
    $r = 12 * $s
    $g.FillEllipse($greenBrush, (188 * $s) - $r, $cy - $r, $r * 2, $r * 2)
  }

  $g.Dispose()
  $out = Join-Path $publicDir $outName
  $bmp.Save($out, [System.Drawing.Imaging.ImageFormat]::Png)
  $bmp.Dispose()
  Write-Host "wrote $out"
}

New-Icon 192 'pwa-192x192.png' $false
New-Icon 512 'pwa-512x512.png' $false
New-Icon 512 'pwa-maskable-512x512.png' $true
New-Icon 180 'apple-touch-icon.png' $true
