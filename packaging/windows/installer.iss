; Inno Setup script for the Mini-Tun Asymmetric Windows client.
; Build:  iscc /DMyAppVersion=0.1.0 packaging\windows\installer.iss
;   (or let packaging/build-installer.sh pass the version from the VERSION file)
; Prereq: the client bundle must exist at dist\flutter-app\ — run
;         packaging/build-windows.sh first.
;
; The GUI self-elevates the agent via UAC at runtime, so the installer itself
; installs per-machine into Program Files but the app does not require the GUI
; to run as admin.

#ifndef MyAppVersion
  #define MyAppVersion "0.0.0"
#endif
#define MyAppName "Mini-Tun Asymmetric"
#define MyAppExe "mini_tun_asymmetric.exe"
#define MyAppPublisher "ZaZaDook"
#define MyAppURL "https://github.com/ZaZaDook/mini-tun-asymmetric"
; Bundle produced by packaging/build-windows.sh, relative to this .iss file.
#define BundleDir "..\..\dist\flutter-app"

[Setup]
AppId={{7F3A2C10-9B4E-4D6A-A1E2-MTA0ASYM0001}
AppName={#MyAppName}
AppVersion={#MyAppVersion}
AppPublisher={#MyAppPublisher}
AppPublisherURL={#MyAppURL}
DefaultDirName={autopf}\{#MyAppName}
DefaultGroupName={#MyAppName}
DisableProgramGroupPage=yes
OutputDir=..\..\dist\packages
OutputBaseFilename=mini-tun-asymmetric-client-{#MyAppVersion}-windows-x64-setup
Compression=lzma2
SolidCompression=yes
WizardStyle=modern
ArchitecturesAllowed=x64compatible
ArchitecturesInstallIn64BitMode=x64compatible
; Per-machine install (Program Files) needs admin; the app itself does not.
PrivilegesRequired=admin
UninstallDisplayIcon={app}\{#MyAppExe}

[Languages]
Name: "english"; MessagesFile: "compiler:Default.isl"
Name: "russian"; MessagesFile: "compiler:Languages\Russian.isl"

[Tasks]
Name: "desktopicon"; Description: "{cm:CreateDesktopIcon}"; GroupDescription: "{cm:AdditionalIcons}"; Flags: unchecked
Name: "startupicon"; Description: "Start {#MyAppName} automatically when I sign in"; GroupDescription: "Startup:"; Flags: unchecked

[Files]
; Whole client bundle (GUI + agent + wintun + flutter/plugin dlls + data\).
Source: "{#BundleDir}\*"; DestDir: "{app}"; Flags: recursesubdirs createallsubdirs ignoreversion

[Icons]
Name: "{group}\{#MyAppName}"; Filename: "{app}\{#MyAppExe}"
Name: "{group}\{cm:UninstallProgram,{#MyAppName}}"; Filename: "{uninstallexe}"
Name: "{autodesktop}\{#MyAppName}"; Filename: "{app}\{#MyAppExe}"; Tasks: desktopicon
Name: "{userstartup}\{#MyAppName}"; Filename: "{app}\{#MyAppExe}"; Tasks: startupicon

[Run]
Filename: "{app}\{#MyAppExe}"; Description: "{cm:LaunchProgram,{#MyAppName}}"; Flags: nowait postinstall skipifsilent

[UninstallDelete]
; The bundle dir is removed automatically. The user profile/config in
; %AppData%\MiniTunAsymmetric (token + profiles) is intentionally left; the
; code below offers to delete it.
Type: filesandordirs; Name: "{app}"

[Code]
procedure CurUninstallStepChanged(CurUninstallStep: TUninstallStep);
var
  AppDataDir: string;
begin
  if CurUninstallStep = usPostUninstall then
  begin
    AppDataDir := ExpandConstant('{userappdata}\MiniTunAsymmetric');
    if DirExists(AppDataDir) then
    begin
      if MsgBox('Remove your saved profiles and auth token too?' + #13#10 +
                AppDataDir, mbConfirmation, MB_YESNO) = IDYES then
        DelTree(AppDataDir, True, True, True);
    end;
  end;
end;
