#import <Cocoa/Cocoa.h>
#import <QuartzCore/QuartzCore.h>
#import <WebKit/WebKit.h>
#include <stdlib.h>
#include <string.h>
#include "webview.h"
#include "_cgo_export.h"

NSView *forge_content_view(void);

// What a page may ask of the network: nothing at all, or pictures alone.
static NSString *const blockAll = @"[{\"trigger\":{\"url-filter\":\".*\"},\"action\":{\"type\":\"block\"}}]";
static NSString *const blockAllButPictures =
	@"[{\"trigger\":{\"url-filter\":\".*\"},\"action\":{\"type\":\"block\"}},"
	 "{\"trigger\":{\"url-filter\":\".*\",\"resource-type\":[\"image\"]},\"action\":{\"type\":\"ignore-previous-rules\"}}]";

static int webModifierBits(NSEventModifierFlags f) {
	int m = 0;
	if (f & (NSEventModifierFlagCommand | NSEventModifierFlagControl)) m |= 1;
	if (f & NSEventModifierFlagShift) m |= 2;
	if (f & NSEventModifierFlagOption) m |= 4;
	return m;
}

@interface ForgeWebView : WKWebView
@property(nonatomic, copy) NSArray<NSValue *> *holeRects; // in top-left local points
@end

@implementation ForgeWebView
- (BOOL)becomeFirstResponder {
	goWebTouched();
	return [super becomeFirstResponder];
}
- (NSView *)hitTest:(NSPoint)point {
	// point is in superview (ForgeView, flipped) coordinates.
	if (self.holeRects.count > 0) {
		NSPoint local = NSMakePoint(point.x - self.frame.origin.x, point.y - self.frame.origin.y);
		for (NSValue *val in self.holeRects) {
			if (NSPointInRect(local, val.rectValue)) {
				return nil;
			}
		}
	}
	return [super hitTest:point];
}
- (BOOL)performKeyEquivalent:(NSEvent *)e {
	NSEventModifierFlags f = e.modifierFlags;
	BOOL cmd = (f & (NSEventModifierFlagCommand | NSEventModifierFlagControl)) != 0;
	BOOL alt = (f & NSEventModifierFlagOption) != 0;
	NSString *chars = e.charactersIgnoringModifiers.lowercaseString;
	// Copy and Select All stay with the page.
	if (cmd && !alt && ([chars isEqualToString:@"c"] || [chars isEqualToString:@"a"])) {
		return [super performKeyEquivalent:e];
	}
	// Forward app shortcuts (Cmd/Ctrl/Alt combinations, Esc, function keys) back to the main window.
	BOOL isEsc = e.keyCode == 53;
	BOOL isFn = (e.keyCode == 122 || e.keyCode == 120 || e.keyCode == 99 || e.keyCode == 118 ||
	             e.keyCode == 96 || e.keyCode == 97 || e.keyCode == 98 || e.keyCode == 100 ||
	             e.keyCode == 101 || e.keyCode == 109 || e.keyCode == 103 || e.keyCode == 111);
	if (cmd || alt || isEsc || isFn) {
		goKey(e.keyCode, (char *)e.charactersIgnoringModifiers.UTF8String, webModifierBits(f));
		return YES;
	}
	return [super performKeyEquivalent:e];
}
@end

@interface ForgeWebDelegate : NSObject <WKNavigationDelegate, WKUIDelegate>
@property(nonatomic) BOOL loading; // the next navigation is the page's own
@end

static NSString *fit;

@implementation ForgeWebDelegate
- (void)webView:(WKWebView *)view
	decidePolicyForNavigationAction:(WKNavigationAction *)action
	                decisionHandler:(void (^)(WKNavigationActionPolicy))decide {
	if (self.loading && action.navigationType == WKNavigationTypeOther) {
		self.loading = NO;
		decide(WKNavigationActionPolicyAllow);
		return;
	}
	NSString *url = action.request.URL.absoluteString;
	if (action.navigationType == WKNavigationTypeLinkActivated && url && [url hasPrefix:@"about:blank#"]) {
		decide(WKNavigationActionPolicyAllow);
		return;
	}
	decide(WKNavigationActionPolicyCancel);
	if (action.navigationType == WKNavigationTypeLinkActivated && url) {
		goWebLink((char *)url.UTF8String);
	}
}
- (void)webView:(WKWebView *)view didFinishNavigation:(WKNavigation *)navigation {
	if (fit.length > 0) {
		[view evaluateJavaScript:fit completionHandler:nil]; // the program's own: allowsContentJavaScript is for the page's
	}
	goWebLoaded();
}
- (WKWebView *)webView:(WKWebView *)view
	createWebViewWithConfiguration:(WKWebViewConfiguration *)configuration
	           forNavigationAction:(WKNavigationAction *)action
	                windowFeatures:(WKWindowFeatures *)features {
	if (action.navigationType == WKNavigationTypeLinkActivated && action.request.URL.absoluteString) {
		goWebLink((char *)action.request.URL.absoluteString.UTF8String);
	}
	return nil;
}
@end

static ForgeWebView *web;
static ForgeWebDelegate *delegate;
static WKContentRuleList *rules[2]; // by whether pictures are allowed
static NSString *waiting;           // the page that waits for its rules
static int waitingPictures;

// load shows a page only under its rules: until they are compiled the page waits, as it must not load without them.
static void load(NSString *page, int pictures) {
	if (!web) {
		return;
	}
	if (!rules[pictures]) {
		waiting = page;
		waitingPictures = pictures;
		return;
	}
	waiting = nil;
	[web.configuration.userContentController removeAllContentRuleLists];
	[web.configuration.userContentController addContentRuleList:rules[pictures]];
	delegate.loading = YES;
	[web loadHTMLString:page baseURL:nil];
}

static void compileRules(int pictures, NSString *source) {
	if (rules[pictures]) {
		return;
	}
	[WKContentRuleListStore.defaultStore compileContentRuleListForIdentifier:pictures ? @"forge-mail-pictures" : @"forge-mail"
	                                                  encodedContentRuleList:source
	                                                       completionHandler:^(WKContentRuleList *list, NSError *error) {
		                                                       if (error || !list) {
			                                                       NSString *msg = error ? error.localizedDescription : @"content rules failed to compile";
			                                                       goWebError((char *)msg.UTF8String);
			                                                       return;
		                                                       }
		                                                       rules[pictures] = list;
		                                                       if (web && waiting && waitingPictures == pictures) {
			                                                       load(waiting, pictures);
		                                                       }
	                                                       }];
}

void forge_web_open(void) {
	@autoreleasepool {
		WKWebViewConfiguration *c = [[WKWebViewConfiguration alloc] init];
		c.websiteDataStore = WKWebsiteDataStore.nonPersistentDataStore;
		c.defaultWebpagePreferences.allowsContentJavaScript = NO;
		web = [[ForgeWebView alloc] initWithFrame:NSZeroRect configuration:c];
		delegate = [[ForgeWebDelegate alloc] init];
		web.navigationDelegate = delegate;
		web.UIDelegate = delegate;
		web.allowsLinkPreview = NO;
		web.wantsLayer = YES;
		web.hidden = YES;
		[forge_content_view() addSubview:web];
		compileRules(0, blockAll);
		compileRules(1, blockAllButPictures);
	}
}

void forge_web_show(const char *page, const char *fitter, int pictures) {
	@autoreleasepool {
		fit = fitter ? [NSString stringWithUTF8String:fitter] : @"";
		NSString *html = page ? [NSString stringWithUTF8String:page] : @"";
		load(html, pictures ? 1 : 0);
	}
}

void forge_web_eval(const char *script) {
	@autoreleasepool {
		if (!web || !script || !*script) {
			return;
		}
		NSString *js = [NSString stringWithUTF8String:script];
		if (js) {
			[web evaluateJavaScript:js completionHandler:nil];
		}
	}
}

// The window's view is flipped, so a frame reads as the program's pixels do, in points.
void forge_web_place(double x, double y, double width, double height, const double *holes, int count) {
	@autoreleasepool {
		if (!web) {
			return;
		}
		CGFloat s = web.window ? web.window.backingScaleFactor : 1.0;
		if (s <= 0) {
			s = 1.0;
		}
		CGFloat viewW = width / s;
		CGFloat viewH = height / s;
		BOOL wider = fabs(web.frame.size.width - viewW) > 0.5;
		[CATransaction begin];
		[CATransaction setDisableActions:YES];
		web.frame = NSMakeRect(x / s, y / s, viewW, viewH);
		web.hidden = NO;
		if (count <= 0) {
			web.holeRects = nil;
			web.layer.mask = nil;
		} else {
			NSMutableArray<NSValue *> *rects = [NSMutableArray arrayWithCapacity:(NSUInteger)count];
			CGMutablePathRef path = CGPathCreateMutable();
			CGPathAddRect(path, NULL, CGRectMake(0, 0, viewW, viewH));
			BOOL layerFlipped = web.layer.geometryFlipped;
			for (int i = 0; i < count; i++) {
				const double *h = holes + i * 4;
				CGFloat hx = h[0] / s, hy = h[1] / s, hw = h[2] / s, hh = h[3] / s;
				[rects addObject:[NSValue valueWithRect:NSMakeRect(hx, hy, hw, hh)]];
				CGFloat maskY = layerFlipped ? hy : (viewH - hy - hh);
				CGPathAddRect(path, NULL, CGRectMake(hx, maskY, hw, hh));
			}
			web.holeRects = rects;
			CAShapeLayer *mask = [CAShapeLayer layer];
			mask.frame = CGRectMake(0, 0, viewW, viewH);
			mask.path = path;
			mask.fillRule = kCAFillRuleEvenOdd;
			web.layer.mask = mask;
			CGPathRelease(path);
		}
		[CATransaction commit];
		if (wider && fit.length > 0) {
			[web evaluateJavaScript:fit completionHandler:nil];
		}
	}
}

void forge_web_hide(void) {
	if (web) {
		web.hidden = YES;
	}
}

void forge_web_close(void) {
	@autoreleasepool {
		if (web) {
			web.navigationDelegate = nil;
			web.UIDelegate = nil;
			[web removeFromSuperview];
			web = nil;
		}
		delegate = nil;
		waiting = nil;
		fit = nil;
	}
}

char *forge_web_title(void) {
	@autoreleasepool {
		if (!web || !web.title) {
			return NULL;
		}
		const char *s = web.title.UTF8String;
		return s ? strdup(s) : NULL;
	}
}
