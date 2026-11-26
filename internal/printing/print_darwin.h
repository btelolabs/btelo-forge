// Native macOS printing bridge (PrintCore / NSPrintPanel).
#include <stddef.h>

char *forge_printers(void);
int forge_printer_exists(const char *name);

typedef struct {
	int ok;        // 1 = print/save chosen, 0 = cancelled, -1 = error
	char *name;
	int copies;
	int collate;
	char *output;  // non-NULL when user chose Save as PDF
	void *settings;
	int settingsLen;
	void *pageFormat;
	int pageFormatLen;
	char *errMsg;
} forgePrintChoice;

forgePrintChoice forge_choose_printer(const char *lastName, int lastCopies, int lastCollate,
                                      const void *lastSettings, int lastSettingsLen,
                                      const void *lastFormat, int lastFormatLen);
void forge_free_print_choice(forgePrintChoice *c);

// forge_print_pdf submits a PDF file to printerName via CorePrinting (safe on any thread).
// Returns NULL on success, or a heap-allocated error message (caller frees).
char *forge_print_pdf(const char *printerName, const char *jobTitle, const char *pdfPath,
                      int copies, int collate,
                      const void *settingsData, int settingsLen,
                      const void *formatData, int formatLen);
void forge_print_free(void *p);
