#import <Cocoa/Cocoa.h>
#import <QuartzCore/QuartzCore.h>
#import <WebKit/WebKit.h>
#include "webview.h"
#include "_cgo_export.h"

NSView *forge_content_view(void);

// What a page may ask of the network: nothing at all, or pictures alone.
static NSString *const blockAll = @"[{\"trigger\":{\"url-filter\":\".*\"},\"action\":{\"type\":\"block\"}}]";
static NSString *const blockAllButPictures =
	@"[{\"trigger\":{\"url-filter\":\".*\"},\"action\":{\"type\":\"block\"}},"
	 "{\"trigger\":{\"url-filter\":\".*\",\"resource-type\":[\"image\"]},\"action\":{\"type\":\"ignore-previous-rules\"}}]";

@interface ForgeWebView : WKWebView
@end

@implementation ForgeWebView
- (BOOL)becomeFirstResponder {
	goWebTouched();
	return [super becomeFirstResponder];
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
	decide(WKNavigationActionPolicyCancel);
	if (action.navigationType == WKNavigationTypeLinkActivated) {
		goWebLink((char *)action.request.URL.absoluteString.UTF8String);
	}
}
- (void)webView:(WKWebView *)view didFinishNavigation:(WKNavigation *)navigation {
	[view evaluateJavaScript:fit completionHandler:nil]; // the program's own: allowsContentJavaScript is for the page's
}
- (WKWebView *)webView:(WKWebView *)view
	createWebViewWithConfiguration:(WKWebViewConfiguration *)configuration
	           forNavigationAction:(WKNavigationAction *)action
	                windowFeatures:(WKWindowFeatures *)features {
	if (action.navigationType == WKNavigationTypeLinkActivated) {
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
	[WKContentRuleListStore.defaultStore compileContentRuleListForIdentifier:pictures ? @"forge-mail-pictures" : @"forge-mail"
	                                                  encodedContentRuleList:source
	                                                       completionHandler:^(WKContentRuleList *list, NSError *error) {
		                                                       rules[pictures] = list;
		                                                       if (list && waiting && waitingPictures == pictures) {
			                                                       load(waiting, pictures);
		                                                       }
	                                                       }];
}

void forge_web_open(void) {
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

void forge_web_show(const char *page, const char *fitter, int pictures) {
	fit = [NSString stringWithUTF8String:fitter];
	load([NSString stringWithUTF8String:page], pictures);
}

// The window's view is flipped, so a frame reads as the program's pixels do, in points.
void forge_web_place(double x, double y, double width, double height, const double *holes, int count) {
	CGFloat s = web.window.backingScaleFactor;
	BOOL wider = web.frame.size.width != width / s;
	web.frame = NSMakeRect(x / s, y / s, width / s, height / s);
	if (wider && fit) {
		[web evaluateJavaScript:fit completionHandler:nil];
	}
	web.hidden = NO;
	if (count == 0) {
		web.layer.mask = nil;
		return;
	}
	CGMutablePathRef path = CGPathCreateMutable();
	CGPathAddRect(path, NULL, CGRectMake(0, 0, width / s, height / s));
	for (int i = 0; i < count; i++) {
		const double *h = holes + i * 4;
		CGPathAddRect(path, NULL, CGRectMake(h[0] / s, h[1] / s, h[2] / s, h[3] / s));
	}
	CAShapeLayer *mask = [CAShapeLayer layer];
	mask.path = path;
	mask.fillRule = kCAFillRuleEvenOdd;
	web.layer.mask = mask;
	CGPathRelease(path);
}

void forge_web_hide(void) { web.hidden = YES; }

void forge_web_close(void) {
	[web removeFromSuperview];
	web = nil;
	delegate = nil;
}
