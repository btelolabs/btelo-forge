// The Cocoa window behind package mac; every function runs on the main thread except bvc_wake.
#include <stddef.h>

int bvc_open(const char *title, int width, int height, int background);
int bvc_poll(void);
void bvc_wait(double seconds);
void bvc_wake(void);
void bvc_size(int *width, int *height);
double bvc_scale(void);
void bvc_present(const void *rgba, int width, int height);
void bvc_title(const char *title);
void bvc_cursor(int kind);
void bvc_text_input(int on, double x, double y, double height);
char *bvc_clipboard_text(void);
void bvc_set_clipboard(const char *text);
void *bvc_clipboard_png(int *length);
char *bvc_clipboard_files(void);
char *bvc_open_panel(const char *title, const char *extensions, int multiple, int folders);
void bvc_close(void);
void bvc_alert(const char *message);
void bvc_free(void *p);
