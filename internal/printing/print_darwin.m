#import <ApplicationServices/ApplicationServices.h>
#import <Cocoa/Cocoa.h>
#include <stdlib.h>
#include <string.h>
#include "print_darwin.h"

static char *dupNSString(NSString *s) {
	if (!s) {
		return NULL;
	}
	const char *u = s.UTF8String;
	return u ? strdup(u) : NULL;
}

static char *dupCFString(CFStringRef s) {
	if (!s) {
		return NULL;
	}
	NSString *ns = (__bridge NSString *)s;
	return dupNSString(ns);
}

static char *statusError(const char *op, OSStatus st) {
	char buf[160];
	snprintf(buf, sizeof(buf), "%s failed (OSStatus %d)", op, (int)st);
	return strdup(buf);
}

char *forge_printers(void) {
	@autoreleasepool {
		CFArrayRef list = NULL;
		OSStatus st = PMServerCreatePrinterList(kPMServerLocal, &list);
		if (st != noErr || !list) {
			return NULL;
		}
		NSMutableArray<NSString *> *names = [NSMutableArray array];
		CFIndex n = CFArrayGetCount(list);
		for (CFIndex i = 0; i < n; i++) {
			PMPrinter p = (PMPrinter)CFArrayGetValueAtIndex(list, i);
			CFStringRef name = PMPrinterGetName(p);
			if (name) {
				[names addObject:(__bridge NSString *)name];
			}
		}
		CFRelease(list);
		if (names.count == 0) {
			return NULL;
		}
		return dupNSString([names componentsJoinedByString:@"\n"]);
	}
}

static PMPrinter findPrinter(CFStringRef targetName) {
	if (!targetName) {
		return NULL;
	}
	CFArrayRef list = NULL;
	if (PMServerCreatePrinterList(kPMServerLocal, &list) != noErr || !list) {
		return NULL;
	}
	PMPrinter found = NULL;
	CFIndex n = CFArrayGetCount(list);
	for (CFIndex i = 0; i < n; i++) {
		PMPrinter p = (PMPrinter)CFArrayGetValueAtIndex(list, i);
		CFStringRef name = PMPrinterGetName(p);
		if (name && CFStringCompare(name, targetName, 0) == kCFCompareEqualTo) {
			found = p;
			PMRetain(found);
			break;
		}
	}
	CFRelease(list);
	return found;
}

int forge_printer_exists(const char *name) {
	@autoreleasepool {
		if (!name || !*name) {
			return 0;
		}
		CFStringRef target = CFStringCreateWithCString(NULL, name, kCFStringEncodingUTF8);
		if (!target) {
			return 0;
		}
		PMPrinter p = findPrinter(target);
		CFRelease(target);
		if (!p) {
			return 0;
		}
		PMRelease(p);
		return 1;
	}
}

forgePrintChoice forge_choose_printer(const char *lastName, int lastCopies, int lastCollate,
                                      const void *lastSettings, int lastSettingsLen,
                                      const void *lastFormat, int lastFormatLen) {
	forgePrintChoice out;
	memset(&out, 0, sizeof(out));
	@autoreleasepool {
		NSPrintInfo *info = [[NSPrintInfo sharedPrintInfo] copy];
		if (!info) {
			info = [[NSPrintInfo alloc] init];
		}
		PMPrintSession session = (PMPrintSession)[info PMPrintSession];
		if (lastSettings && lastSettingsLen > 0) {
			CFDataRef d = CFDataCreate(NULL, (const UInt8 *)lastSettings, lastSettingsLen);
			PMPrintSettings restored = NULL;
			if (d && PMPrintSettingsCreateWithDataRepresentation(d, &restored) == noErr && restored) {
				PMPrintSettings cur = (PMPrintSettings)[info PMPrintSettings];
				if (cur) {
					PMCopyPrintSettings(restored, cur);
					[info updateFromPMPrintSettings];
				}
				PMRelease(restored);
			}
			if (d) {
				CFRelease(d);
			}
		}
		if (lastFormat && lastFormatLen > 0) {
			CFDataRef d = CFDataCreate(NULL, (const UInt8 *)lastFormat, lastFormatLen);
			PMPageFormat restored = NULL;
			if (d && PMPageFormatCreateWithDataRepresentation(d, &restored) == noErr && restored) {
				PMPageFormat cur = (PMPageFormat)[info PMPageFormat];
				if (cur) {
					PMCopyPageFormat(restored, cur);
					[info updateFromPMPageFormat];
				}
				PMRelease(restored);
			}
			if (d) {
				CFRelease(d);
			}
		}
		if (lastName && *lastName) {
			NSPrinter *pr = [NSPrinter printerWithName:@(lastName)];
			if (pr) {
				info.printer = pr;
			}
		}
		if (lastCopies > 0) {
			info.dictionary[NSPrintCopies] = @(lastCopies);
		}
		info.dictionary[NSPrintMustCollate] = @(lastCollate != 0);

		NSPrintPanel *panel = [NSPrintPanel printPanel];
		panel.options = NSPrintPanelShowsCopies | NSPrintPanelShowsPageSetupAccessory |
		                NSPrintPanelShowsOrientation | NSPrintPanelShowsPaperSize |
		                NSPrintPanelShowsScaling | NSPrintPanelShowsPreview;
		NSInteger resp = [panel runModalWithPrintInfo:info];
		if (resp != NSModalResponseOK) {
			out.ok = 0;
			return out;
		}
		out.ok = 1;
		out.name = dupNSString(info.printer.name);
		NSNumber *copiesNum = info.dictionary[NSPrintCopies];
		out.copies = copiesNum ? MAX(copiesNum.intValue, 1) : 1;
		NSNumber *collateNum = info.dictionary[NSPrintMustCollate];
		out.collate = collateNum && collateNum.boolValue ? 1 : 0;

		if ([info.jobDisposition isEqualToString:NSPrintSaveJob]) {
			NSURL *saveURL = info.dictionary[NSPrintJobSavingURL];
			if (saveURL && saveURL.path.length > 0) {
				out.output = dupNSString(saveURL.path);
			}
		}
		(void)session;
		PMPrintSettings ps = (PMPrintSettings)[info PMPrintSettings];
		if (ps) {
			CFDataRef data = NULL;
			if (PMPrintSettingsCreateDataRepresentation(ps, &data, kPMDataFormatXMLDefault) == noErr && data) {
				CFIndex len = CFDataGetLength(data);
				if (len > 0) {
					out.settings = malloc((size_t)len);
					memcpy(out.settings, CFDataGetBytePtr(data), (size_t)len);
					out.settingsLen = (int)len;
				}
				CFRelease(data);
			}
		}
		PMPageFormat pf = (PMPageFormat)[info PMPageFormat];
		if (pf) {
			CFDataRef data = NULL;
			if (PMPageFormatCreateDataRepresentation(pf, &data, kPMDataFormatXMLDefault) == noErr && data) {
				CFIndex len = CFDataGetLength(data);
				if (len > 0) {
					out.pageFormat = malloc((size_t)len);
					memcpy(out.pageFormat, CFDataGetBytePtr(data), (size_t)len);
					out.pageFormatLen = (int)len;
				}
				CFRelease(data);
			}
		}
		return out;
	}
}

void forge_free_print_choice(forgePrintChoice *c) {
	if (!c) {
		return;
	}
	free(c->name);
	free(c->output);
	free(c->settings);
	free(c->pageFormat);
	free(c->errMsg);
	memset(c, 0, sizeof(*c));
}

char *forge_print_pdf(const char *printerName, const char *jobTitle, const char *pdfPath,
                      int copies, int collate,
                      const void *settingsData, int settingsLen,
                      const void *formatData, int formatLen) {
	@autoreleasepool {
		if (!printerName || !*printerName) {
			return strdup("no printer specified");
		}
		CFStringRef nameRef = CFStringCreateWithCString(NULL, printerName, kCFStringEncodingUTF8);
		PMPrinter printer = findPrinter(nameRef);
		CFRelease(nameRef);
		if (!printer) {
			char buf[256];
			snprintf(buf, sizeof(buf), "printer %s not found", printerName);
			return strdup(buf);
		}

		PMPrintSession session = NULL;
		OSStatus st = PMCreateSession(&session);
		if (st != noErr) {
			PMRelease(printer);
			return statusError("PMCreateSession", st);
		}
		PMSessionSetCurrentPMPrinter(session, printer);

		PMPrintSettings settings = NULL;
		if (settingsData && settingsLen > 0) {
			CFDataRef d = CFDataCreate(NULL, (const UInt8 *)settingsData, settingsLen);
			if (d) {
				PMPrintSettingsCreateWithDataRepresentation(d, &settings);
				CFRelease(d);
			}
		}
		if (!settings) {
			st = PMCreatePrintSettings(&settings);
			if (st == noErr) {
				PMSessionDefaultPrintSettings(session, settings);
			}
		}

		PMPageFormat format = NULL;
		if (formatData && formatLen > 0) {
			CFDataRef d = CFDataCreate(NULL, (const UInt8 *)formatData, formatLen);
			if (d) {
				PMPageFormatCreateWithDataRepresentation(d, &format);
				CFRelease(d);
			}
		}
		if (!format) {
			st = PMCreatePageFormat(&format);
			if (st == noErr) {
				PMSessionDefaultPageFormat(session, format);
			}
		}

		if (settings) {
			if (copies > 0) {
				PMSetCopies(settings, (UInt32)copies, false);
			}
			PMSetCollate(settings, collate != 0);
			if (jobTitle && *jobTitle) {
				CFStringRef jt = CFStringCreateWithCString(NULL, jobTitle, kCFStringEncodingUTF8);
				if (jt) {
					PMPrintSettingsSetJobName(settings, jt);
					CFRelease(jt);
				}
			}
			PMSessionValidatePrintSettings(session, settings, kPMDontWantBoolean);
		}
		if (format) {
			PMSessionValidatePageFormat(session, format, kPMDontWantBoolean);
		}

		NSURL *fileURL = [NSURL fileURLWithPath:@(pdfPath)];
		st = PMPrinterPrintWithFile(printer, settings, format, CFSTR("application/pdf"), (__bridge CFURLRef)fileURL);

		if (format) {
			PMRelease(format);
		}
		if (settings) {
			PMRelease(settings);
		}
		PMRelease(session);
		PMRelease(printer);

		if (st != noErr) {
			return statusError("PMPrinterPrintWithFile", st);
		}
		return NULL;
	}
}

void forge_print_free(void *p) {
	free(p);
}
