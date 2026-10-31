// Explorer's thumbnails for the app's own files. Explorer can only be given pictures by a COM object in a DLL, and Go
// makes no such DLL without cgo, so this is the one piece of the Windows app that is C. The app embeds what it builds
// (build.ps1) and registers it with the file types. Windows runs it in a process of its own, for files it did not
// choose, so it trusts nothing it reads, and it needs no C runtime.
//
// A saved file ends with a JPEG and a footer (internal/thumb): only those bytes are read, whatever the file's size.
// A file without them fails here, and Explorer shows its icon.

#define COBJMACROS
#define WIN32_LEAN_AND_MEAN
#include <stddef.h>
#include <windows.h>
#include <objidl.h>
#include <wincodec.h>

// {6F2B7C1E-93A4-4D58-B1E6-2C7D40A9F315}: keep it in step with thumbnailerCLSID in internal/win.
static const CLSID clsidThumbnailer = {0x6f2b7c1e, 0x93a4, 0x4d58, {0xb1, 0xe6, 0x2c, 0x7d, 0x40, 0xa9, 0xf3, 0x15}};
static const IID iidThumbnailProvider = {0xe357fccd, 0xa995, 0x4576, {0xb0, 0x1f, 0x23, 0x46, 0x30, 0x15, 0x4e, 0x96}};
static const IID iidInitializeWithStream = {0xb824b49d, 0x22ac, 0x4161, {0xac, 0x8a, 0x99, 0x16, 0xe8, 0xfa, 0x3f, 0x7f}};
static const IID iidUnknown = {0, 0, 0, {0xc0, 0, 0, 0, 0, 0, 0, 0x46}};
static const IID iidClassFactory = {1, 0, 0, {0xc0, 0, 0, 0, 0, 0, 0, 0x46}};
static const IID iidWICFactory = {0xec5ec8a9, 0xc395, 0x4314, {0x9c, 0x77, 0x54, 0xd7, 0xa9, 0x35, 0xff, 0x70}};
static const CLSID clsidWICFactory = {0xcacaf262, 0x9370, 0x4615, {0xa1, 0x3b, 0x9f, 0x55, 0x39, 0xda, 0x4c, 0x0a}};
static const GUID formatBGR32 = {0x6fddc324, 0x4e03, 0x4bfe, {0xb1, 0x85, 0x3d, 0x77, 0x76, 0x8d, 0xc9, 0x0e}};

#define WTSAT_RGB 2

// sameGUID is IsEqualGUID without the C runtime's memcmp.
static BOOL sameGUID(const GUID *a, const GUID *b) {
	const BYTE *x = (const BYTE *)a, *y = (const BYTE *)b;
	for (int i = 0; i < (int)sizeof(GUID); i++) {
		if (x[i] != y[i]) {
			return FALSE;
		}
	}
	return TRUE;
}

// The footer: the JPEG's length, the picture's width, height and duration, and this mark. Four little-endian
// 32-bit numbers, then eight bytes.
enum { footerSize = 24, maxPicture = 1 << 20 };
static const char footerMark[8] = {'B', 'T', 'F', 'P', 'I', 'C', '0', '1'};

typedef struct {
	IUnknownVtbl *initialize; // IInitializeWithStream, laid out as an IUnknown with one method more
	IUnknownVtbl *provider;   // IThumbnailProvider, likewise
	LONG refs;
	IStream *stream;
} Thumbnailer;

static LONG liveObjects;

static Thumbnailer *fromInitialize(void *p) { return (Thumbnailer *)p; }
static Thumbnailer *fromProvider(void *p) { return (Thumbnailer *)((char *)p - offsetof(Thumbnailer, provider)); }

static HRESULT thumbnailerQuery(Thumbnailer *t, REFIID iid, void **out) {
	if (sameGUID(iid, &iidUnknown) || sameGUID(iid, &iidInitializeWithStream)) {
		*out = &t->initialize;
	} else if (sameGUID(iid, &iidThumbnailProvider)) {
		*out = &t->provider;
	} else {
		*out = NULL;
		return E_NOINTERFACE;
	}
	InterlockedIncrement(&t->refs);
	return S_OK;
}

static ULONG thumbnailerRelease(Thumbnailer *t) {
	LONG left = InterlockedDecrement(&t->refs);
	if (left == 0) {
		if (t->stream) {
			IStream_Release(t->stream);
		}
		HeapFree(GetProcessHeap(), 0, t);
		InterlockedDecrement(&liveObjects);
	}
	return (ULONG)left;
}

static HRESULT STDMETHODCALLTYPE initQuery(void *p, REFIID iid, void **out) { return thumbnailerQuery(fromInitialize(p), iid, out); }
static ULONG STDMETHODCALLTYPE initAddRef(void *p) { return (ULONG)InterlockedIncrement(&fromInitialize(p)->refs); }
static ULONG STDMETHODCALLTYPE initRelease(void *p) { return thumbnailerRelease(fromInitialize(p)); }

static HRESULT STDMETHODCALLTYPE initInitialize(void *p, IStream *stream, DWORD mode) {
	Thumbnailer *t = fromInitialize(p);
	(void)mode;
	if (t->stream) {
		return HRESULT_FROM_WIN32(ERROR_ALREADY_INITIALIZED);
	}
	t->stream = stream;
	IStream_AddRef(stream);
	return S_OK;
}

static HRESULT STDMETHODCALLTYPE provQuery(void *p, REFIID iid, void **out) { return thumbnailerQuery(fromProvider(p), iid, out); }
static ULONG STDMETHODCALLTYPE provAddRef(void *p) { return (ULONG)InterlockedIncrement(&fromProvider(p)->refs); }
static ULONG STDMETHODCALLTYPE provRelease(void *p) { return thumbnailerRelease(fromProvider(p)); }

static UINT32 number(const BYTE *b) { return (UINT32)b[0] | (UINT32)b[1] << 8 | (UINT32)b[2] << 16 | (UINT32)b[3] << 24; }

// readAll reads exactly n bytes.
static HRESULT readAll(IStream *s, void *to, ULONG n) {
	ULONG got = 0;
	HRESULT hr = IStream_Read(s, to, n, &got);
	if (FAILED(hr)) {
		return hr;
	}
	return got == n ? S_OK : E_FAIL;
}

// readPicture reads the JPEG at the file's end into memory the caller frees.
static HRESULT readPicture(IStream *s, BYTE **picture, UINT32 *length) {
	LARGE_INTEGER move;
	ULARGE_INTEGER size;
	BYTE footer[footerSize];
	move.QuadPart = 0;
	HRESULT hr = IStream_Seek(s, move, STREAM_SEEK_END, &size);
	if (FAILED(hr)) {
		return hr;
	}
	if (size.QuadPart < footerSize) {
		return E_FAIL;
	}
	move.QuadPart = -(LONGLONG)footerSize;
	if (FAILED(hr = IStream_Seek(s, move, STREAM_SEEK_END, NULL)) || FAILED(hr = readAll(s, footer, footerSize))) {
		return hr;
	}
	for (int i = 0; i < 8; i++) {
		if (footer[16 + i] != (BYTE)footerMark[i]) {
			return E_FAIL;
		}
	}
	UINT32 n = number(footer);
	if (n == 0 || n > maxPicture || (ULONGLONG)n + footerSize > size.QuadPart) {
		return E_FAIL;
	}
	move.QuadPart = -(LONGLONG)(n + footerSize);
	if (FAILED(hr = IStream_Seek(s, move, STREAM_SEEK_END, NULL))) {
		return hr;
	}
	BYTE *data = HeapAlloc(GetProcessHeap(), 0, n);
	if (!data) {
		return E_OUTOFMEMORY;
	}
	if (FAILED(hr = readAll(s, data, n))) {
		HeapFree(GetProcessHeap(), 0, data);
		return hr;
	}
	*picture = data;
	*length = n;
	return S_OK;
}

// toBitmap draws the source, at most box pixels each way, into a new top-down bitmap.
static HRESULT toBitmap(IWICImagingFactory *wic, IWICBitmapSource *source, UINT box, HBITMAP *out) {
	UINT w, h;
	HRESULT hr = IWICBitmapSource_GetSize(source, &w, &h);
	if (FAILED(hr)) {
		return hr;
	}
	if (w == 0 || h == 0) {
		return E_FAIL;
	}
	IWICBitmapScaler *scaler = NULL;
	if (w > box || h > box) {
		UINT sw = box, sh = box;
		if (w >= h) {
			sh = max(1, (UINT)((ULONGLONG)h * box / w));
		} else {
			sw = max(1, (UINT)((ULONGLONG)w * box / h));
		}
		if (FAILED(hr = IWICImagingFactory_CreateBitmapScaler(wic, &scaler))) {
			return hr;
		}
		if (FAILED(hr = IWICBitmapScaler_Initialize(scaler, source, sw, sh, WICBitmapInterpolationModeFant))) {
			IWICBitmapScaler_Release(scaler);
			return hr;
		}
		source = (IWICBitmapSource *)scaler;
		w = sw;
		h = sh;
	}
	BITMAPINFOHEADER info = {.biSize = sizeof info, .biWidth = (LONG)w, .biHeight = -(LONG)h, .biPlanes = 1, .biBitCount = 32, .biCompression = BI_RGB};
	void *bits = NULL;
	HBITMAP bitmap = CreateDIBSection(NULL, (BITMAPINFO *)&info, DIB_RGB_COLORS, &bits, NULL, 0);
	if (!bitmap) {
		hr = E_OUTOFMEMORY;
	} else {
		hr = IWICBitmapSource_CopyPixels(source, NULL, w * 4, w * h * 4, bits);
		if (SUCCEEDED(hr)) {
			*out = bitmap;
		} else {
			DeleteObject(bitmap);
		}
	}
	if (scaler) {
		IWICBitmapScaler_Release(scaler);
	}
	return hr;
}

static HRESULT draw(IStream *s, UINT box, HBITMAP *out) {
	BYTE *picture = NULL;
	UINT32 length = 0;
	HRESULT hr = readPicture(s, &picture, &length);
	if (FAILED(hr)) {
		return hr;
	}
	IWICImagingFactory *wic = NULL;
	IWICStream *memory = NULL;
	IWICBitmapDecoder *decoder = NULL;
	IWICBitmapFrameDecode *frame = NULL;
	IWICFormatConverter *converter = NULL;
	hr = CoCreateInstance(&clsidWICFactory, NULL, CLSCTX_INPROC_SERVER, &iidWICFactory, (void **)&wic);
	if (SUCCEEDED(hr)) {
		hr = IWICImagingFactory_CreateStream(wic, &memory);
	}
	if (SUCCEEDED(hr)) {
		hr = IWICStream_InitializeFromMemory(memory, picture, length);
	}
	if (SUCCEEDED(hr)) {
		hr = IWICImagingFactory_CreateDecoderFromStream(wic, (IStream *)memory, NULL, WICDecodeMetadataCacheOnDemand, &decoder);
	}
	if (SUCCEEDED(hr)) {
		hr = IWICBitmapDecoder_GetFrame(decoder, 0, &frame);
	}
	if (SUCCEEDED(hr)) {
		hr = IWICImagingFactory_CreateFormatConverter(wic, &converter);
	}
	if (SUCCEEDED(hr)) {
		hr = IWICFormatConverter_Initialize(converter, (IWICBitmapSource *)frame, &formatBGR32, WICBitmapDitherTypeNone, NULL, 0, WICBitmapPaletteTypeCustom);
	}
	if (SUCCEEDED(hr)) {
		hr = toBitmap(wic, (IWICBitmapSource *)converter, box, out);
	}
	if (converter) {
		IWICFormatConverter_Release(converter);
	}
	if (frame) {
		IWICBitmapFrameDecode_Release(frame);
	}
	if (decoder) {
		IWICBitmapDecoder_Release(decoder);
	}
	if (memory) {
		IWICStream_Release(memory);
	}
	if (wic) {
		IWICImagingFactory_Release(wic);
	}
	HeapFree(GetProcessHeap(), 0, picture);
	return hr;
}

static HRESULT STDMETHODCALLTYPE provGetThumbnail(void *p, UINT box, HBITMAP *bitmap, DWORD *alpha) {
	Thumbnailer *t = fromProvider(p);
	if (!t->stream || !bitmap || !alpha || box == 0) {
		return E_INVALIDARG;
	}
	*bitmap = NULL;
	*alpha = WTSAT_RGB;
	return draw(t->stream, box, bitmap);
}

static const struct {
	HRESULT(STDMETHODCALLTYPE *QueryInterface)(void *, REFIID, void **);
	ULONG(STDMETHODCALLTYPE *AddRef)(void *);
	ULONG(STDMETHODCALLTYPE *Release)(void *);
	HRESULT(STDMETHODCALLTYPE *Initialize)(void *, IStream *, DWORD);
} initializeTable = {initQuery, initAddRef, initRelease, initInitialize};

static const struct {
	HRESULT(STDMETHODCALLTYPE *QueryInterface)(void *, REFIID, void **);
	ULONG(STDMETHODCALLTYPE *AddRef)(void *);
	ULONG(STDMETHODCALLTYPE *Release)(void *);
	HRESULT(STDMETHODCALLTYPE *GetThumbnail)(void *, UINT, HBITMAP *, DWORD *);
} providerTable = {provQuery, provAddRef, provRelease, provGetThumbnail};

// The class factory: one object, never freed.

static HRESULT STDMETHODCALLTYPE factoryQuery(IClassFactory *f, REFIID iid, void **out) {
	if (sameGUID(iid, &iidUnknown) || sameGUID(iid, &iidClassFactory)) {
		*out = f;
		return S_OK;
	}
	*out = NULL;
	return E_NOINTERFACE;
}

static ULONG STDMETHODCALLTYPE factoryAddRef(IClassFactory *f) {
	(void)f;
	return 2;
}

static ULONG STDMETHODCALLTYPE factoryRelease(IClassFactory *f) {
	(void)f;
	return 1;
}

static HRESULT STDMETHODCALLTYPE factoryCreate(IClassFactory *f, IUnknown *outer, REFIID iid, void **out) {
	(void)f;
	*out = NULL;
	if (outer) {
		return CLASS_E_NOAGGREGATION;
	}
	Thumbnailer *t = HeapAlloc(GetProcessHeap(), HEAP_ZERO_MEMORY, sizeof *t);
	if (!t) {
		return E_OUTOFMEMORY;
	}
	t->initialize = (IUnknownVtbl *)&initializeTable;
	t->provider = (IUnknownVtbl *)&providerTable;
	t->refs = 1;
	InterlockedIncrement(&liveObjects);
	HRESULT hr = thumbnailerQuery(t, iid, out);
	thumbnailerRelease(t);
	return hr;
}

static HRESULT STDMETHODCALLTYPE factoryLock(IClassFactory *f, BOOL lock) {
	(void)f;
	if (lock) {
		InterlockedIncrement(&liveObjects);
	} else {
		InterlockedDecrement(&liveObjects);
	}
	return S_OK;
}

static IClassFactoryVtbl factoryTable = {factoryQuery, factoryAddRef, factoryRelease, factoryCreate, factoryLock};
static IClassFactory factory = {&factoryTable};

HRESULT STDAPICALLTYPE DllGetClassObject(REFCLSID clsid, REFIID iid, void **out) {
	if (!sameGUID(clsid, &clsidThumbnailer)) {
		*out = NULL;
		return CLASS_E_CLASSNOTAVAILABLE;
	}
	return factoryQuery(&factory, iid, out);
}

HRESULT STDAPICALLTYPE DllCanUnloadNow(void) { return liveObjects == 0 ? S_OK : S_FALSE; }
