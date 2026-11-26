$ErrorActionPreference = 'Stop'
Add-Type -AssemblyName System.Windows.Forms
Add-Type -AssemblyName System.Drawing
Add-Type -Path @($env:LENA_FADE_BRAND_CODE, (Join-Path $env:LENA_FADE_FOLDER_ASSETS 'FolderDropZone.cs')) -ReferencedAssemblies System.dll,System.Windows.Forms.dll,System.Drawing.dll
[System.Windows.Forms.Application]::EnableVisualStyles()
$form = New-Object System.Windows.Forms.Form
$form.Text = 'S-кривая · 4 сек'
$form.ClientSize = New-Object System.Drawing.Size(660,505)
$form.StartPosition = 'CenterScreen'
$form.AutoScroll = $true
$form.FormBorderStyle = 'FixedDialog'
$form.MaximizeBox = $false
$form.MinimizeBox = $false
$form.BackColor = [System.Drawing.ColorTranslator]::FromHtml('#15232d')
$form.ForeColor = [System.Drawing.ColorTranslator]::FromHtml('#edf5f5')
$form.Font = New-Object System.Drawing.Font('Segoe UI',10)
$form.Icon = [System.Drawing.Icon]::ExtractAssociatedIcon($env:LENA_FADE_EXE)
$heading = New-Object System.Windows.Forms.Label
$heading.Text = 'Плавное начало музыки'
$heading.Font = New-Object System.Drawing.Font('Segoe UI',19,[System.Drawing.FontStyle]::Bold)
$heading.SetBounds(26,22,605,40)
$form.Controls.Add($heading)
$dropZone = [FolderDropZone]::new((Join-Path $env:LENA_FADE_FOLDER_ASSETS 'windows-folder.png'))
$dropZone.SetBounds(28,72,604,222)
$dropZone.ShowState('Перетащите папку сюда', $false, $false)
$form.Controls.Add($dropZone)
$folders = New-Object System.Windows.Forms.ComboBox
$folders.SetBounds(28,309,486,30)
$folders.DropDownStyle = 'DropDown'
$folders.MaxDropDownItems = 6
$folders.IntegralHeight = $false
$folders.DropDownHeight = 150
foreach ($path in @($env:LENA_FADE_HISTORY | ConvertFrom-Json)) {
    if (-not [string]::IsNullOrWhiteSpace([string]$path)) { [void]$folders.Items.Add([string]$path) }
}
if ($folders.Items.Count -gt 0) { $folders.SelectedIndex = 0 }
else { $folders.Text = [Environment]::GetFolderPath('MyMusic') }
$form.Controls.Add($folders)
$browse = New-Object System.Windows.Forms.Button
$browse.Text = 'Обзор…'
$browse.ForeColor = [System.Drawing.ColorTranslator]::FromHtml('#102a2a')
$browse.SetBounds(526,307,106,32)
$browse.Add_Click({
    $dialog = New-Object System.Windows.Forms.FolderBrowserDialog
    $dialog.Description = 'Выберите папку с исходными аудиофайлами'
    if ([IO.Directory]::Exists($folders.Text)) { $dialog.SelectedPath = $folders.Text }
    if ($dialog.ShowDialog($form) -eq 'OK') { Set-SelectedFolder $dialog.SelectedPath }
    $dialog.Dispose()
})
$form.Controls.Add($browse)
$note = New-Object System.Windows.Forms.Label
$note.Text = 'Результаты появятся в подпапке «S-кривая 4 сек».'
$note.SetBounds(28,349,600,25)
$form.Controls.Add($note)
$brand = [IGBrandControl]::new($env:LENA_FADE_BRAND_IMAGE)
$brand.SetBounds(28,425,112,62)
$form.Controls.Add($brand)
$start = New-Object System.Windows.Forms.Button
$start.Text = 'Обработать'
$start.BackColor = [System.Drawing.ColorTranslator]::FromHtml('#63d7c1')
$start.ForeColor = [System.Drawing.ColorTranslator]::FromHtml('#102a2a')
$start.FlatStyle = 'Flat'
$start.SetBounds(422,437,126,38)
$versionLabel = New-Object System.Windows.Forms.Label
$versionLabel.Text = 'v' + $env:LENA_FADE_VERSION
$versionLabel.ForeColor = [System.Drawing.ColorTranslator]::FromHtml('#91a8aa')
$versionLabel.SetBounds(560,6,90,18)
$form.Controls.Add($versionLabel)
$updateLabel = New-Object System.Windows.Forms.Label
$updateLabel.Text = 'Проверяем обновления…'
$updateLabel.ForeColor = [System.Drawing.ColorTranslator]::FromHtml('#91a8aa')
$updateLabel.SetBounds(28,386,486,25)
$form.Controls.Add($updateLabel)
$updateButton = New-Object System.Windows.Forms.Button
$updateButton.Text = 'Обновить'
$updateButton.SetBounds(526,380,106,32)
$updateButton.Visible = $false
$updateButton.Add_Click({
    $updateButton.Enabled = $false
    [IO.File]::WriteAllText($env:LENA_FADE_SELECTION,'{"action":"update"}',(New-Object System.Text.UTF8Encoding($false)))
    $form.DialogResult = 'OK'
    $form.Close()
})
$form.Controls.Add($updateButton)
$updateTimer = New-Object System.Windows.Forms.Timer
$updateTimer.Interval = 250
$updateTimer.Add_Tick({
    if (-not [IO.File]::Exists($env:LENA_FADE_UPDATE_STATUS)) { return }
    try { $status = [IO.File]::ReadAllText($env:LENA_FADE_UPDATE_STATUS) | ConvertFrom-Json } catch { return }
    $updateTimer.Stop()
    if ($status.available) {
        $updateLabel.Text = 'Доступна версия ' + $status.latest
        $updateLabel.ForeColor = [System.Drawing.ColorTranslator]::FromHtml('#efbd4b')
        $updateButton.Visible = $true
    } elseif ($status.error) {
        $updateLabel.Text = 'Проверка обновлений недоступна. Можно работать.'
    } else { $updateLabel.Text = 'Установлена актуальная версия ' + $status.current }
})
if ($env:LENA_MUSIC_CHECK -ne '1') { $updateTimer.Start() }
$form.Add_FormClosed({ $updateTimer.Stop(); $updateTimer.Dispose() })
$recursive = New-Object System.Windows.Forms.CheckBox
if ($env:LENA_MUSIC_CHECK -eq '1') {
    $form.Text = 'Проверка музыки'
    $versionLabel.Visible = $false
    $updateLabel.Visible = $false
    $heading.Text = 'Проверка музыкальной библиотеки'
    $heading.Font = New-Object System.Drawing.Font('Segoe UI',18,[System.Drawing.FontStyle]::Bold)
    $note.Text = 'Отчёт и история проверок — в подпапке «Проверка музыки».'
    $recursive.Text = 'Включая подпапки'
    $recursive.Checked = $true
    $recursive.SetBounds(28,378,500,27)
    $form.Controls.Add($recursive)
    $start.Text = 'Проверить'
}
$start.Add_Click({
    $selected = $folders.Text.Trim()
    if (-not [IO.Directory]::Exists($selected)) {
        [void][System.Windows.Forms.MessageBox]::Show($form,'Выберите существующую папку.','Папка не найдена')
        return
    }
    try {
        $selected = [IO.Path]::GetFullPath($selected)
        $value = $selected
        if ($env:LENA_MUSIC_CHECK -eq '1') { $value = @{folder=$selected; recursive=[bool]$recursive.Checked} | ConvertTo-Json -Compress }
        [IO.File]::WriteAllText($env:LENA_FADE_SELECTION,$value,(New-Object System.Text.UTF8Encoding($false)))
        $form.DialogResult = 'OK'
        $form.Close()
    } catch {
        [void][System.Windows.Forms.MessageBox]::Show($form,$_.Exception.Message,'Не удалось запустить')
    }
})
$form.Controls.Add($start)
$cancel = New-Object System.Windows.Forms.Button
$cancel.Text = 'Отмена'
$cancel.ForeColor = [System.Drawing.ColorTranslator]::FromHtml('#102a2a')
$cancel.SetBounds(554,437,80,38)
$cancel.DialogResult = 'Cancel'
$form.Controls.Add($cancel)
$form.AcceptButton = $start
$form.CancelButton = $cancel

function Update-FolderZone {
    if ($folders.Text -and $folders.Text -eq $dropZone.Tag) {
        $dropZone.ShowState('Папка выбрана', $false, $true)
    } else {
        $dropZone.ShowState('Перетащите папку сюда', $false, $false)
    }
}
function Set-SelectedFolder($path) {
    $dropZone.Tag = $path
    $folders.Text = $path
    Update-FolderZone
}
$folders.Add_TextChanged({
    $start.Enabled = -not [string]::IsNullOrWhiteSpace($folders.Text)
    Update-FolderZone
})
$folders.Add_SelectionChangeCommitted({ Set-SelectedFolder ([string]$folders.SelectedItem) })
$start.Enabled = -not [string]::IsNullOrWhiteSpace($folders.Text)

function Get-DroppedFolder($data) {
    try {
        if ($null -eq $data -or -not $data.GetDataPresent([System.Windows.Forms.DataFormats]::FileDrop)) { return $null }
        $paths = @($data.GetData([System.Windows.Forms.DataFormats]::FileDrop))
        if ($paths.Count -ne 1 -or -not [IO.Directory]::Exists([string]$paths[0])) { return $null }
        return [IO.Path]::GetFullPath([string]$paths[0])
    } catch { return $null }
}
$dragOver = {
    param($sender, $eventArgs)
    $eventArgs.Effect = [System.Windows.Forms.DragDropEffects]::None
    if ((Get-DroppedFolder $eventArgs.Data) -and (($eventArgs.AllowedEffect -band [System.Windows.Forms.DragDropEffects]::Copy) -ne 0)) {
        $eventArgs.Effect = [System.Windows.Forms.DragDropEffects]::Copy
        $dropZone.ShowState('Отпустите, чтобы выбрать', $true, $false)
    } else { Update-FolderZone }
}
$dropFolder = {
    param($sender, $eventArgs)
    $path = Get-DroppedFolder $eventArgs.Data
    if ($path -and (($eventArgs.AllowedEffect -band [System.Windows.Forms.DragDropEffects]::Copy) -ne 0)) {
        Set-SelectedFolder $path
    } else { Update-FolderZone }
}
foreach ($control in (@($form) + @($form.Controls))) {
    $control.AllowDrop = $true
    $control.Add_DragEnter($dragOver)
    $control.Add_DragOver($dragOver)
    $control.Add_DragLeave({ Update-FolderZone })
    $control.Add_DragDrop($dropFolder)
}
$form.Add_Shown({
    $form.Activate()
    [IO.File]::WriteAllText($env:LENA_FADE_READY,'ready')
})
$workArea = [System.Windows.Forms.Screen]::PrimaryScreen.WorkingArea
if ($form.Height -gt $workArea.Height) { $form.Height = $workArea.Height }
[void]$form.ShowDialog()
$form.Dispose()
