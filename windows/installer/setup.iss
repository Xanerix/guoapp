#define ExecutableName Slug + ".exe"

[Setup]
AppId={#AppId}
AppName={#AppName}
AppVersion={#AppVersion}
AppVerName={#AppName} {#AppVersion}
VersionInfoVersion={#NumericVersion}
AppMutex={#AppId}
DefaultDirName={autopf}\{#AppId}
DisableProgramGroupPage=yes
PrivilegesRequired=lowest
PrivilegesRequiredOverridesAllowed=dialog
ArchitecturesAllowed=x64compatible
ArchitecturesInstallIn64BitMode=x64compatible
MinVersion=10.0
SetupIconFile=..\runner\resources\app_icon.ico
UninstallDisplayIcon={app}\{#ExecutableName}
UninstallDisplayName={#AppName}
OutputDir={#OutputDir}
OutputBaseFilename={#OutputBaseName}
Compression=lzma2/ultra64
SolidCompression=yes
WizardStyle=modern

[Languages]
Name: "chinesesimplified"; MessagesFile: "ChineseSimplified.isl"

[Tasks]
Name: "desktopicon"; Description: "{cm:CreateDesktopIcon}"; GroupDescription: "{cm:AdditionalIcons}"

[Dirs]
Name: "{app}"; Flags: uninsalwaysuninstall

[Files]
Source: "{#SourceDir}\*"; DestDir: "{app}"; Excludes: "\{#BundleExecutable}"; Flags: ignoreversion recursesubdirs createallsubdirs
Source: "{#SourceDir}\{#BundleExecutable}"; DestDir: "{app}"; DestName: "{#ExecutableName}"; Flags: ignoreversion

[Icons]
Name: "{autoprograms}\{#AppName}"; Filename: "{app}\{#ExecutableName}"
Name: "{autodesktop}\{#AppName}"; Filename: "{app}\{#ExecutableName}"; Tasks: desktopicon

[Run]
Filename: "{app}\{#ExecutableName}"; Description: "{cm:LaunchProgram,{#StringChange(AppName, '&', '&&')}}"; Flags: nowait postinstall skipifsilent

[Code]
var
  DeleteLocalData: Boolean;

procedure InitializeUninstallProgressForm();
var
  OptionsPage: TNewNotebookPage;
  Prompt, DataNote: TNewStaticText;
  DataCheckBox: TNewCheckBox;
  UninstallButton: TNewButton;
  PageName, PageDescription: String;
  CancelEnabled: Boolean;
  CancelResult: Integer;
begin
  if UninstallSilent then
    Exit;

  OptionsPage := TNewNotebookPage.Create(UninstallProgressForm);
  OptionsPage.Notebook := UninstallProgressForm.InnerNotebook;
  OptionsPage.Parent := UninstallProgressForm.InnerNotebook;
  OptionsPage.Align := alClient;

  Prompt := TNewStaticText.Create(UninstallProgressForm);
  Prompt.Parent := OptionsPage;
  Prompt.Left := UninstallProgressForm.ProgressBar.Left;
  Prompt.Width := UninstallProgressForm.ProgressBar.Width;
  Prompt.WordWrap := True;
  Prompt.Caption := '默认保留本地数据，重新安装后可继续使用。';

  DataCheckBox := TNewCheckBox.Create(UninstallProgressForm);
  DataCheckBox.Parent := OptionsPage;
  DataCheckBox.Top := Prompt.Top + Prompt.Height + ScaleY(16);
  DataCheckBox.Left := Prompt.Left;
  DataCheckBox.Width := Prompt.Width;
  DataCheckBox.Height := ScaleY(22);
  DataCheckBox.Caption := '同时清除本地数据';

  DataNote := TNewStaticText.Create(UninstallProgressForm);
  DataNote.Parent := OptionsPage;
  DataNote.Left := Prompt.Left + ScaleX(18);
  DataNote.Top := DataCheckBox.Top + DataCheckBox.Height + ScaleY(4);
  DataNote.Width := Prompt.Left + Prompt.Width - DataNote.Left;
  DataNote.WordWrap := True;
  DataNote.Caption := '包括设置、追剧、收藏、缓存和默认目录中的下载，清除后无法恢复。';

  UninstallButton := TNewButton.Create(UninstallProgressForm);
  UninstallButton.Parent := UninstallProgressForm;
  UninstallButton.Width := UninstallProgressForm.CancelButton.Width;
  UninstallButton.Height := UninstallProgressForm.CancelButton.Height;
  UninstallButton.Top := UninstallProgressForm.CancelButton.Top;
  UninstallButton.Left := UninstallProgressForm.CancelButton.Left - UninstallButton.Width - ScaleX(10);
  UninstallButton.Caption := '卸载(&U)';
  UninstallButton.ModalResult := mrOk;
  UninstallButton.Default := True;

  PageName := UninstallProgressForm.PageNameLabel.Caption;
  PageDescription := UninstallProgressForm.PageDescriptionLabel.Caption;
  CancelEnabled := UninstallProgressForm.CancelButton.Enabled;
  CancelResult := UninstallProgressForm.CancelButton.ModalResult;

  UninstallProgressForm.PageNameLabel.Caption := '卸载选项';
  UninstallProgressForm.PageDescriptionLabel.Caption := '选择卸载 {#AppName} 时是否保留本地数据。';
  UninstallProgressForm.InnerNotebook.ActivePage := OptionsPage;
  UninstallProgressForm.CancelButton.Enabled := True;
  UninstallProgressForm.CancelButton.ModalResult := mrCancel;
  UninstallButton.TabOrder := 0;

  if UninstallProgressForm.ShowModal() = mrCancel then
    Abort;

  DeleteLocalData := DataCheckBox.Checked;
  UninstallButton.Visible := False;
  UninstallProgressForm.CancelButton.Enabled := CancelEnabled;
  UninstallProgressForm.CancelButton.ModalResult := CancelResult;
  UninstallProgressForm.PageNameLabel.Caption := PageName;
  UninstallProgressForm.PageDescriptionLabel.Caption := PageDescription;
  UninstallProgressForm.InnerNotebook.ActivePage := UninstallProgressForm.InstallingPage;
end;

procedure CurUninstallStepChanged(CurUninstallStep: TUninstallStep);
begin
  if (CurUninstallStep = usPostUninstall) and DeleteLocalData then
    DelTree(ExpandConstant('{userappdata}\{#AppId}'), True, True, True);
end;
