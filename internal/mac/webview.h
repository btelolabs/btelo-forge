// The WKWebView behind package mac's WebView; every function runs on the main thread.
void forge_web_open(void);
void forge_web_show(const char *page, const char *fit, int pictures);
void forge_web_eval(const char *script);
void forge_web_place(double x, double y, double width, double height, const double *holes, int count);
void forge_web_hide(void);
void forge_web_close(void);
char *forge_web_title(void);
