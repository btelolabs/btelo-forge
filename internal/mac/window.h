// The Cocoa window behind package mac; every function runs on the main thread except forge_wake.
#include <stddef.h>

int forge_open(const char *title, int width, int height, int background);
int forge_poll(void);
void forge_wait(double seconds);
void forge_wake(void);
void forge_size(int *width, int *height);
double forge_scale(void);
int forge_key_window(void);
void forge_present(const void *rgba, int width, int height);
void forge_title(const char *title);
void forge_cursor(int kind);
void forge_text_input(int on, double x, double y, double height);
char *forge_languages(void);
char *forge_clipboard_text(void);
void forge_set_clipboard(const char *text);
void *forge_clipboard_png(int *length);
char *forge_clipboard_files(void);
char *forge_open_panel(const char *title, const char *extensions, int multiple, int folders);
void forge_close(void);
void forge_alert(const char *message);
void forge_free(void *p);
