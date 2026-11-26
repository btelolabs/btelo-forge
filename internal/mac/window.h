// The Cocoa window, overlays, hotkeys, clipboard and file operations behind package mac.
// Every function runs on the main thread except forge_wake and forge_overlay_present.
#include <stddef.h>
#include <stdint.h>

int forge_open(const char *title, int width, int height, int background);
int forge_poll(void);
void forge_wait(double seconds);
void forge_wake(void);
void forge_size(int *width, int *height);
double forge_scale(void);
int forge_key_window(void);
void forge_present(const void *rgba, int width, int height);
void forge_title(const char *title);
int forge_system_dark(void);
void forge_set_dark(int dark);
void forge_cursor(int kind);
void forge_text_input(int on, double x, double y, double height);
char *forge_languages(void);
char *forge_clipboard_text(void);
void forge_set_clipboard(const char *text);
void *forge_clipboard_png(int *length);
int forge_set_clipboard_png(const void *pngData, int length);
char *forge_clipboard_files(void);
int forge_set_clipboard_files(const char *newlinePaths);
char *forge_trash_files(const char *newlinePaths);
char *forge_open_panel(const char *title, const char *extensions, int multiple, int folders);
void forge_close(void);
void forge_alert(const char *message);
void forge_free(void *p);

// Window visibility, capture exclusion and system-wide hotkeys.
void forge_minimize(void);
void forge_restore(void);
void forge_hide(void);
int forge_shown(void);
void forge_exclude_capture(int on);
void forge_clear_hotkeys(void);
int forge_register_hotkey(int id, uint32_t macKeyCode, int ctrl, int alt, int shift);

// Desktop pointer & transparent always-on-top overlay panels.
int forge_has_gui_session(void);
void forge_pointer_at(int *outX, int *outY);
void *forge_overlay_open(int id, int x, int y, int width, int height, int activate, int clickThrough, int excluded);
double forge_overlay_scale(void *handle);
int forge_overlay_present(void *handle, const void *rgba, int width, int height);
void forge_overlay_set_click_through(void *handle, int on);
void forge_overlay_take_keyboard(void *handle, int on);
void forge_overlay_raise(void *handle);
void forge_overlay_move(void *handle, int x, int y, int width, int height);
void forge_overlay_cursor(void *handle, int kind);
void forge_overlay_text_input(void *handle, int on, double x, double y, double height);
void forge_overlay_close(void *handle);
