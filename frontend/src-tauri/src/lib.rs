use std::path::{Path, PathBuf};
use std::process::{Command, Output};
use tauri::Manager;

const DEFAULT_NIC_NAME: &str = "VPN";
#[cfg(target_os = "windows")]
const CREATE_NO_WINDOW: u32 = 0x08000000;

#[tauri::command]
fn launch_game(path: String) -> Result<(), String> {
    if path.trim().is_empty() {
        return Err("请先选择 WE8 游戏程序".into());
    }
    let mut command = Command::new(path);
    spawn_command(&mut command).map_err(|error| format!("无法启动游戏：{error}"))?;
    Ok(())
}

#[tauri::command]
fn connect_vpn(
    host: String,
    port: u16,
    hub: String,
    username: String,
    password: String,
    nic_name: Option<String>,
) -> Result<(), String> {
    validate_identifier(&hub, "虚拟 HUB")?;
    validate_identifier(&username, "VPN 用户名")?;
    if host.trim().is_empty() || password.is_empty() || port == 0 {
        return Err("VPN 连接参数不完整".into());
    }

    let vpncmd = locate_vpncmd()?;

    let nic_list = run_vpncmd(&vpncmd, &["localhost", "/CLIENT", "/CMD", "NicList"])?;
    let nic = resolve_nic(&vpncmd, &nic_list, nic_name)?;

    let account = format!("WEL-{}", username);
    validate_identifier(&account, "VPN 连接名称")?;
    let server = format!("{host}:{port}");

    let _ = run_vpncmd(
        &vpncmd,
        &["localhost", "/CLIENT", "/CMD", "AccountDelete", &account],
    );
    run_vpncmd(
        &vpncmd,
        &[
            "localhost",
            "/CLIENT",
            "/CMD",
            "AccountCreate",
            &account,
            &format!("/SERVER:{server}"),
            &format!("/HUB:{hub}"),
            &format!("/USERNAME:{username}"),
            &format!("/NICNAME:{nic}"),
        ],
    )?;
    run_vpncmd(
        &vpncmd,
        &[
            "localhost",
            "/CLIENT",
            "/CMD",
            "AccountPasswordSet",
            &account,
            &format!("/PASSWORD:{password}"),
            "/TYPE:standard",
        ],
    )?;
    run_vpncmd(
        &vpncmd,
        &["localhost", "/CLIENT", "/CMD", "AccountConnect", &account],
    )?;
    Ok(())
}

#[tauri::command]
fn disconnect_vpn(username: String) -> Result<(), String> {
    validate_identifier(&username, "VPN 用户名")?;
    let vpncmd = locate_vpncmd()?;
    let account = format!("WEL-{username}");
    validate_identifier(&account, "VPN 连接名称")?;
    let _ = run_vpncmd(
        &vpncmd,
        &["localhost", "/CLIENT", "/CMD", "AccountDisconnect", &account],
    );
    let _ = run_vpncmd(
        &vpncmd,
        &["localhost", "/CLIENT", "/CMD", "AccountDelete", &account],
    );
    Ok(())
}

fn validate_identifier(value: &str, label: &str) -> Result<(), String> {
    if value.is_empty()
        || value.len() > 96
        || !value
            .bytes()
            .all(|byte| byte.is_ascii_alphanumeric() || matches!(byte, b'.' | b'_' | b'-'))
    {
        return Err(format!("{label}格式不正确"));
    }
    Ok(())
}

fn resolve_nic(vpncmd: &Path, nic_list: &Output, preferred: Option<String>) -> Result<String, String> {
    let candidates = nic_candidates(preferred);
    let nic_list_text = String::from_utf8_lossy(&nic_list.stdout);
    if let Some(existing) = candidates
        .iter()
        .find(|candidate| nic_list_contains(&nic_list_text, candidate))
    {
        return Ok(existing.clone());
    }

    let mut last_error = None;
    for candidate in candidates {
        match run_vpncmd(vpncmd, &["localhost", "/CLIENT", "/CMD", "NicCreate", &candidate]) {
            Ok(_) => return Ok(candidate),
            Err(error) => {
                let refreshed = run_vpncmd(vpncmd, &["localhost", "/CLIENT", "/CMD", "NicList"])?;
                let refreshed_text = String::from_utf8_lossy(&refreshed.stdout);
                if nic_list_contains(&refreshed_text, &candidate) {
                    return Ok(candidate);
                }
                if !is_nic_name_unavailable_error(&error) {
                    return Err(error);
                }
                last_error = Some(error);
            }
        }
    }

    Err(last_error.unwrap_or_else(|| "无法创建 SoftEther 虚拟网卡".into()))
}

fn nic_candidates(preferred: Option<String>) -> Vec<String> {
    let mut candidates = Vec::new();
    if let Some(name) = preferred.map(|value| value.trim().to_string()) {
        if is_softether_nic_name(&name) {
            candidates.push(name);
        }
    }
    for index in 1..=127 {
        let name = if index == 1 {
            DEFAULT_NIC_NAME.to_string()
        } else {
            format!("VPN{index}")
        };
        if !candidates.iter().any(|candidate| candidate == &name) {
            candidates.push(name);
        }
    }
    candidates
}

fn is_softether_nic_name(value: &str) -> bool {
    if value == "VPN" {
        return true;
    }
    value
        .strip_prefix("VPN")
        .and_then(|suffix| suffix.parse::<u8>().ok())
        .is_some_and(|number| (2..=127).contains(&number))
}

fn nic_list_contains(output: &str, nic: &str) -> bool {
    output.lines().any(|line| {
        line.split('|')
            .any(|part| part.trim().eq_ignore_ascii_case(nic))
    })
}

fn is_nic_name_unavailable_error(error: &str) -> bool {
    error.contains("错误代码: 32")
        || error.contains("Error code: 32")
        || error.contains("specified name")
        || error.contains("指定名称")
}

fn locate_vpncmd() -> Result<PathBuf, String> {
    #[cfg(target_os = "windows")]
    {
        let candidates = [
            PathBuf::from(r"C:\Program Files\SoftEther VPN Client\vpncmd.exe"),
            PathBuf::from(r"C:\Program Files (x86)\SoftEther VPN Client\vpncmd.exe"),
        ];
        if let Some(path) = candidates.into_iter().find(|path| path.is_file()) {
            return Ok(path);
        }
        return Ok(PathBuf::from("vpncmd.exe"));
    }

    #[cfg(not(target_os = "windows"))]
    {
        Err("SoftEther 自动连接仅支持 Windows 客户端".into())
    }
}

fn run_vpncmd(vpncmd: &Path, args: &[&str]) -> Result<Output, String> {
    let mut command = Command::new(vpncmd);
    command.args(args);
    let output = output_command(&mut command).map_err(|error| format!("无法启动 SoftEther 客户端：{error}"))?;
    if !output.status.success() {
        let command = args
            .windows(2)
            .find_map(|items| (items[0] == "/CMD").then_some(items[1]))
            .unwrap_or("vpncmd");
        let stdout = String::from_utf8_lossy(&output.stdout);
        let stderr = String::from_utf8_lossy(&output.stderr);
        let detail = [stdout.trim(), stderr.trim()]
            .into_iter()
            .filter(|value| !value.is_empty())
            .collect::<Vec<_>>()
            .join("\n");
        return Err(if detail.is_empty() {
            format!("SoftEther 客户端命令执行失败：{command}")
        } else {
            format!("SoftEther 客户端命令执行失败：{command}\n{detail}")
        });
    }
    Ok(output)
}

#[cfg(target_os = "windows")]
fn output_command(command: &mut Command) -> std::io::Result<Output> {
    use std::os::windows::process::CommandExt;

    command.creation_flags(CREATE_NO_WINDOW).output()
}

#[cfg(not(target_os = "windows"))]
fn output_command(command: &mut Command) -> std::io::Result<Output> {
    command.output()
}

#[cfg(target_os = "windows")]
fn spawn_command(command: &mut Command) -> std::io::Result<std::process::Child> {
    use std::os::windows::process::CommandExt;

    command.creation_flags(CREATE_NO_WINDOW).spawn()
}

#[cfg(not(target_os = "windows"))]
fn spawn_command(command: &mut Command) -> std::io::Result<std::process::Child> {
    command.spawn()
}

pub fn run() {
    tauri::Builder::default()
        .setup(|app| {
            if let Some(window) = app.get_webview_window("main") {
                let _ = window.set_title("WEL职业联盟对战平台");
            }
            Ok(())
        })
        .invoke_handler(tauri::generate_handler![
            launch_game,
            connect_vpn,
            disconnect_vpn
        ])
        .run(tauri::generate_context!())
        .expect("error while running WE8 Platform");
}
