use std::sync::Mutex;
use std::time::Duration;
use tauri::image::Image;
use tauri::{Manager, Theme, WindowEvent};
use tauri_plugin_shell::process::CommandChild;
use tauri_plugin_shell::ShellExt;

/// Brand icons embedded at compile time.
const ICON_LIGHT_BYTES: &[u8] = include_bytes!("../../../brand/light/icon-256.png");
const ICON_DARK_BYTES: &[u8] = include_bytes!("../../../brand/dark/icon-256.png");

fn icon_bytes_for_theme(theme: Theme) -> &'static [u8] {
    match theme {
        Theme::Dark => ICON_LIGHT_BYTES,
        _ => ICON_DARK_BYTES,
    }
}

fn build_theme_icon(theme: Theme) -> Option<Image<'static>> {
    Image::from_bytes(icon_bytes_for_theme(theme)).ok()
}

fn apply_theme_icon(app: &tauri::AppHandle, theme: Theme) {
    let Some(img) = build_theme_icon(theme) else {
        log::warn!("Failed to decode theme icon bytes");
        return;
    };
    if let Some(window) = app.get_webview_window("main") {
        if let Err(e) = window.set_icon(img.clone()) {
            log::warn!("Failed to set window icon: {}", e);
        }
    }
    if let Some(tray) = app.tray_by_id("main-tray") {
        if let Err(e) = tray.set_icon(Some(img)) {
            log::warn!("Failed to set tray icon: {}", e);
        }
    }
    log::info!("Theme icon applied for theme {:?}", theme);
}

struct AppState {
    agent_process: Mutex<Option<CommandChild>>,
    agent_port: Mutex<u16>,
    management_token: String,
}

impl Default for AppState {
    fn default() -> Self {
        Self {
            agent_process: Mutex::new(None),
            agent_port: Mutex::new(8787),
            management_token: generate_management_token(),
        }
    }
}

/// Generate a random 32-char hex token for local management API auth.
fn generate_management_token() -> String {
    use std::time::{SystemTime, UNIX_EPOCH};
    let seed = SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .unwrap_or_default()
        .as_nanos();
    let mut state = seed as u64;
    let mut bytes = [0u8; 16];
    for b in &mut bytes {
        state = state.wrapping_mul(6364136223846793005).wrapping_add(1442695040888963407);
        *b = (state >> 33) as u8;
    }
    hex_encode(&bytes)
}

fn hex_encode(bytes: &[u8]) -> String {
    let mut s = String::with_capacity(bytes.len() * 2);
    for b in bytes {
        s.push_str(&format!("{:02x}", b));
    }
    s
}

fn start_agent(app: &tauri::AppHandle, token: &str) -> Result<CommandChild, String> {
    let port = 8787u16;
    let addr = format!("127.0.0.1:{}", port);

    let (_rx, child) = app
        .shell()
        .sidecar("vibebridge-agent")
        .map_err(|e| format!("Failed to find sidecar binary: {}", e))?
        .args([
            "--addr", &addr,
            "--tray=false",
            "--management-token", token,
        ])
        .spawn()
        .map_err(|e| format!("Failed to spawn agent: {}", e))?;

    log::info!("VibeBridge agent started on {} (pid={})", addr, child.pid());
    Ok(child)
}

/// Check if the agent is responding on its health endpoint (no auth required).
async fn check_agent_health(port: u16) -> bool {
    let url = format!("http://127.0.0.1:{}/healthz", port);
    reqwest::Client::new()
        .get(&url)
        .timeout(Duration::from_secs(2))
        .send()
        .await
        .map(|r| r.status().is_success())
        .unwrap_or(false)
}

#[tauri::command]
async fn get_agent_status(state: tauri::State<'_, AppState>) -> Result<serde_json::Value, String> {
    let port = *state.agent_port.lock().unwrap();
    let token = state.management_token.clone();
    let running = check_agent_health(port).await;

    let info = if running {
        let url = format!("http://127.0.0.1:{}/agent/info?token={}", port, token);
        match reqwest::Client::new()
            .get(&url)
            .timeout(Duration::from_secs(2))
            .send()
            .await
        {
            Ok(resp) if resp.status().is_success() => {
                resp.json::<serde_json::Value>()
                    .await
                    .unwrap_or(serde_json::json!({}))
            }
            _ => serde_json::json!({}),
        }
    } else {
        serde_json::json!({})
    };

    Ok(serde_json::json!({
        "running": running,
        "port": port,
        "info": info,
    }))
}

#[tauri::command]
async fn restart_agent(
    app: tauri::AppHandle,
    state: tauri::State<'_, AppState>,
) -> Result<(), String> {
    if let Some(child) = state.agent_process.lock().unwrap().take() {
        let _ = child.kill();
    }
    tokio::time::sleep(Duration::from_millis(500)).await;
    let token = state.management_token.clone();
    match start_agent(&app, &token) {
        Ok(child) => {
            *state.agent_process.lock().unwrap() = Some(child);
            Ok(())
        }
        Err(e) => Err(e),
    }
}

#[tauri::command]
fn get_ws_url(state: tauri::State<'_, AppState>) -> String {
    let port = *state.agent_port.lock().unwrap();
    format!("ws://127.0.0.1:{}/ws", port)
}

#[tauri::command]
fn get_http_url(state: tauri::State<'_, AppState>) -> String {
    let port = *state.agent_port.lock().unwrap();
    format!("http://127.0.0.1:{}", port)
}

#[tauri::command]
async fn fetch_pairing_code(state: tauri::State<'_, AppState>) -> Result<serde_json::Value, String> {
    let port = *state.agent_port.lock().unwrap();
    let token = state.management_token.clone();
    let url = format!("http://127.0.0.1:{}/agent/pairing/code?token={}", port, token);
    reqwest::Client::new()
        .get(&url)
        .timeout(Duration::from_secs(5))
        .send()
        .await
        .map_err(|e| format!("Failed to fetch pairing code: {}", e))?
        .json::<serde_json::Value>()
        .await
        .map_err(|e| format!("Failed to parse pairing response: {}", e))
}

pub fn run() {
    env_logger::init();

    tauri::Builder::default()
        .plugin(tauri_plugin_shell::init())
        .plugin(tauri_plugin_process::init())
        .plugin(tauri_plugin_autostart::init(
            tauri_plugin_autostart::MacosLauncher::LaunchAgent,
            Some(vec!["--autostart"]),
        ))
        .plugin(tauri_plugin_notification::init())
        .manage(AppState::default())
        .invoke_handler(tauri::generate_handler![
            get_agent_status,
            restart_agent,
            get_ws_url,
            get_http_url,
            fetch_pairing_code,
        ])
        .setup(|app| {
            log::info!("VibeBridge desktop app starting...");

            let app_handle = app.handle().clone();
            let state: tauri::State<AppState> = app.state();
            let token = state.management_token.clone();

            match start_agent(&app_handle, &token) {
                Ok(child) => {
                    log::info!("Agent started successfully");
                    *state.agent_process.lock().unwrap() = Some(child);
                }
                Err(e) => {
                    log::error!("Failed to start agent: {}. Running in standalone mode.", e);
                }
            }

            let initial_theme = app
                .get_webview_window("main")
                .and_then(|w| w.theme().ok())
                .unwrap_or(Theme::Light);
            let initial_icon = build_theme_icon(initial_theme);

            let mut tray_builder = tauri::tray::TrayIconBuilder::with_id("main-tray")
                .tooltip("VibeBridge")
                .menu(&tauri::menu::Menu::with_items(
                    app,
                    &[
                        &tauri::menu::MenuItem::with_id(app, "show", "Show VibeBridge", true, None::<&str>)?,
                        &tauri::menu::MenuItem::with_id(app, "quit", "Quit", true, None::<&str>)?,
                    ],
                )?)
                .on_menu_event(|app, event| match event.id.as_ref() {
                    "show" => {
                        if let Some(window) = app.get_webview_window("main") {
                            let _ = window.show();
                            let _ = window.set_focus();
                        }
                    }
                    "quit" => {
                        app.exit(0);
                    }
                    _ => {}
                });
            if let Some(icon) = initial_icon.clone() {
                tray_builder = tray_builder.icon(icon);
            } else {
                tray_builder = tray_builder.icon(app.default_window_icon().unwrap().clone());
            }
            let _tray = tray_builder.build(app)?;

            if let Some(window) = app.get_webview_window("main") {
                if let Some(icon) = initial_icon {
                    let _ = window.set_icon(icon);
                }
            }

            Ok(())
        })
        .on_window_event(|window, event| match event {
            WindowEvent::ThemeChanged(theme) => {
                apply_theme_icon(window.app_handle(), *theme);
            }
            WindowEvent::CloseRequested { api, .. } => {
                let _ = window.hide();
                api.prevent_close();
            }
            _ => {}
        })
        .run(tauri::generate_context!())
        .expect("error while running tauri application");
}
