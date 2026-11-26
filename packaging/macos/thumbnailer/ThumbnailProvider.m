// Finder QuickLook thumbnail extension for Btelo Forge's own formats (.btcanvas, .btcut, .btmind).
// Every saved file ends with a JPEG and a 24-byte footer (internal/thumb):
//
//   JPEG | length(u32 LE) | width(u32 LE) | height(u32 LE) | milliseconds(u32 LE) | "BTFPIC01"
//
// Only the last bytes of the file are read, regardless of the file's total size, matching
// packaging/windows/thumbnailer/thumbnailer.c.

#import <Foundation/Foundation.h>
#import <QuickLookThumbnailing/QuickLookThumbnailing.h>
#import <ImageIO/ImageIO.h>
#import <CoreGraphics/CoreGraphics.h>
#include <fcntl.h>
#include <stdint.h>
#include <string.h>
#include <sys/stat.h>
#include <unistd.h>

enum {
	kFooterSize = 24,
	kMaxPicture = 1 << 20,
};

static const char kFooterMark[8] = {'B', 'T', 'F', 'P', 'I', 'C', '0', '1'};

static uint32_t readU32LE(const uint8_t *b) {
	return (uint32_t)b[0] | ((uint32_t)b[1] << 8) | ((uint32_t)b[2] << 16) | ((uint32_t)b[3] << 24);
}

static NSData *readEmbeddedJPEG(NSURL *fileURL) {
	const char *path = fileURL.fileSystemRepresentation;
	if (!path) {
		return nil;
	}
	int fd = open(path, O_RDONLY);
	if (fd < 0) {
		return nil;
	}
	struct stat st;
	if (fstat(fd, &st) != 0 || st.st_size < kFooterSize) {
		close(fd);
		return nil;
	}
	uint8_t footer[kFooterSize];
	if (pread(fd, footer, kFooterSize, st.st_size - kFooterSize) != kFooterSize) {
		close(fd);
		return nil;
	}
	if (memcmp(footer + 16, kFooterMark, sizeof(kFooterMark)) != 0) {
		close(fd);
		return nil;
	}
	uint32_t length = readU32LE(footer);
	if (length == 0 || length > kMaxPicture || (off_t)(length + kFooterSize) > st.st_size) {
		close(fd);
		return nil;
	}
	NSMutableData *jpeg = [NSMutableData dataWithLength:length];
	if (!jpeg) {
		close(fd);
		return nil;
	}
	off_t offset = st.st_size - (off_t)kFooterSize - (off_t)length;
	ssize_t got = pread(fd, jpeg.mutableBytes, length, offset);
	close(fd);
	if (got != (ssize_t)length) {
		return nil;
	}
	return jpeg;
}

@interface ForgeThumbnailProvider : QLThumbnailProvider
@end

@implementation ForgeThumbnailProvider

- (void)provideThumbnailForFileRequest:(QLFileThumbnailRequest *)request
                     completionHandler:(void (^)(QLThumbnailReply * _Nullable, NSError * _Nullable))handler {
	NSData *jpeg = readEmbeddedJPEG(request.fileURL);
	if (!jpeg) {
		handler(nil, [NSError errorWithDomain:@"com.btelolabs.forge.thumbnailer" code:1 userInfo:nil]);
		return;
	}
	CGImageSourceRef src = CGImageSourceCreateWithData((__bridge CFDataRef)jpeg, NULL);
	if (!src) {
		handler(nil, [NSError errorWithDomain:@"com.btelolabs.forge.thumbnailer" code:2 userInfo:nil]);
		return;
	}
	CGImageRef image = CGImageSourceCreateImageAtIndex(src, 0, NULL);
	CFRelease(src);
	if (!image) {
		handler(nil, [NSError errorWithDomain:@"com.btelolabs.forge.thumbnailer" code:3 userInfo:nil]);
		return;
	}

	CGFloat imgW = (CGFloat)CGImageGetWidth(image);
	CGFloat imgH = (CGFloat)CGImageGetHeight(image);
	CGSize maxSize = request.maximumSize;
	CGFloat scale = MIN(maxSize.width / MAX(imgW, 1.0), maxSize.height / MAX(imgH, 1.0));
	if (scale > 1.0) {
		scale = 1.0;
	}
	CGSize drawSize = CGSizeMake(MAX(1.0, floor(imgW * scale)), MAX(1.0, floor(imgH * scale)));

	QLThumbnailReply *reply = [QLThumbnailReply replyWithContextSize:drawSize
	                                                  drawingBlock:^BOOL(CGContextRef ctx) {
		if (!ctx) {
			CGImageRelease(image);
			return NO;
		}
		CGContextSetInterpolationQuality(ctx, kCGInterpolationHigh);
		CGContextDrawImage(ctx, CGRectMake(0, 0, drawSize.width, drawSize.height), image);
		CGImageRelease(image);
		return YES;
	}];
	handler(reply, nil);
}

@end
