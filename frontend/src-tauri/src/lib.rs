use std::path::{Path, PathBuf};
use std::process::{Command, Output};
use tauri::Manager;

const DEFAULT_NIC_NAME: &str = "VPNWE8";

#[tauri::command]
fn launch_game(path: String) -> Result<(), String> {
    if path.trim().is_empty() {
        return Err("请先选择 WE8 游戏程序".into());
    }
    Command::new(path).spawn().map_err(|error| format!("无法启动游戏：{error}"))?;
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

    let nic = nic_name
        .filter(|value| !value.trim().is_empty())
        .unwrap_or_else(|| DEFAULT_NIC_NAME.to_string());
    validate_identifier(&nic, "虚拟网卡")?;
    let vpncmd = locate_vpncmd()?;

    let nic_list = run_vpncmd(&vpncmd, &["localhost", "/CLIENT", "/CMD", "NicList"])?;
    let nic_exists = String::from_utf8_lossy(&nic_list.stdout).contains(&nic);
    if !nic_exists {
        run_vpncmd(&vpncmd, &["localhost", "/CLIENT", "/CMD", "NicCreate", &nic])?;
    }

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
    let output = Command::new(vpncmd)
        .args(args)
        .output()
        .map_err(|error| format!("无法启动 SoftEther 客户端：{error}"))?;
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

pub fn run() {
    tauri::Builder::default()
        .plugin(tauri_plugin_dialog::init())
        .plugin(tauri_plugin_opener::init())
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
