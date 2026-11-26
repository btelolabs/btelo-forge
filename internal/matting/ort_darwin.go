//go:build darwin

package matting

/*
#include <dlfcn.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>

// ONNX Runtime C API v24 function pointer signatures, indexed by their ABI-stable slots in OrtApi.
typedef const char* (*ort_get_error_msg_fn)(void *status);
typedef void (*ort_release_fn)(void *obj);
typedef void* (*ort_create_env_fn)(int log_level, const char *logid, void **out);
typedef void* (*ort_create_session_fn)(void *env, const char *model_path, void *options, void **out);
typedef void* (*ort_run_fn)(void *sess, void *run_options, const char *const *input_names, const void *const *inputs, size_t input_len,
	const char *const *output_names, size_t output_names_len, void **outputs);
typedef void* (*ort_create_options_fn)(void **out);
typedef void* (*ort_create_tensor_fn)(const void *info, void *data, size_t data_len, const int64_t *shape, size_t shape_len, int elem_type, void **out);
typedef void* (*ort_tensor_data_fn)(void *value, void **out);
typedef void* (*ort_create_cpu_info_fn)(int allocator_type, int mem_type, void **out);
typedef void* (*ort_append_coreml_fn)(void *options, uint32_t coreml_flags);

typedef struct {
	const void *const *(*GetApi)(uint32_t version);
	const char *(*GetVersionString)(void);
} ort_api_base;

typedef const ort_api_base *(*ort_get_api_base_fn)(void);

static int ort_open_lib(const char *path, uint32_t version, void **handle_out, const void *const **table_out, void **coreml_out, char **err_out) {
	void *h = dlopen(path, RTLD_NOW | RTLD_LOCAL);
	if (!h) {
		const char *e = dlerror();
		*err_out = strdup(e ? e : "dlopen failed");
		return 0;
	}
	ort_get_api_base_fn get_base = (ort_get_api_base_fn)dlsym(h, "OrtGetApiBase");
	if (!get_base) {
		dlclose(h);
		*err_out = strdup("symbol OrtGetApiBase not found");
		return 0;
	}
	const ort_api_base *base = get_base();
	if (!base || !base->GetApi) {
		dlclose(h);
		*err_out = strdup("OrtGetApiBase returned NULL");
		return 0;
	}
	const void *const *table = base->GetApi(version);
	if (!table) {
		dlclose(h);
		*err_out = NULL; // caller formats version mismatch
		return -1;
	}
	*handle_out = h;
	*table_out = table;
	*coreml_out = dlsym(h, "OrtSessionOptionsAppendExecutionProvider_CoreML");
	return 1;
}

static char *ort_take_error(const void *const *table, void *status) {
	if (!status) {
		return NULL;
	}
	ort_get_error_msg_fn get_msg = (ort_get_error_msg_fn)table[2];
	ort_release_fn release_status = (ort_release_fn)table[93];
	const char *msg = get_msg(status);
	char *dup = strdup(msg ? msg : "unknown error");
	release_status(status);
	return dup;
}

static char *ort_create_env(const void *const *table, int level, const char *logid, void **out) {
	ort_create_env_fn fn = (ort_create_env_fn)table[3];
	return ort_take_error(table, fn(level, logid, out));
}

static char *ort_create_cpu_info(const void *const *table, int allocator_type, int mem_type, void **out) {
	ort_create_cpu_info_fn fn = (ort_create_cpu_info_fn)table[69];
	return ort_take_error(table, fn(allocator_type, mem_type, out));
}

static char *ort_create_session(const void *const *table, void *coreml_sym, void *env, const char *model_path, int use_coreml, void **sess_out) {
	ort_create_options_fn create_opts = (ort_create_options_fn)table[10];
	ort_release_fn release_opts = (ort_release_fn)table[100];
	ort_create_session_fn create_sess = (ort_create_session_fn)table[7];

	void *opts = NULL;
	char *err = ort_take_error(table, create_opts(&opts));
	if (err) {
		return err;
	}
	if (use_coreml && coreml_sym) {
		ort_append_coreml_fn append_coreml = (ort_append_coreml_fn)coreml_sym;
		err = ort_take_error(table, append_coreml(opts, 0));
		if (err) {
			release_opts(opts);
			return err;
		}
	}
	err = ort_take_error(table, create_sess(env, model_path, opts, sess_out));
	release_opts(opts);
	return err;
}

static char *ort_run_matte(const void *const *table, void *info, void *sess,
	float *input_data, size_t input_bytes, const int64_t *shape, size_t shape_len,
	float *output_copy, size_t output_elems) {
	ort_create_tensor_fn create_tensor = (ort_create_tensor_fn)table[49];
	ort_run_fn run_fn = (ort_run_fn)table[9];
	ort_tensor_data_fn tensor_data = (ort_tensor_data_fn)table[51];
	ort_release_fn release_value = (ort_release_fn)table[96];

	void *input = NULL;
	char *err = ort_take_error(table, create_tensor(info, input_data, input_bytes, shape, shape_len, 1, &input));
	if (err) {
		return err;
	}
	const char *in_names[1] = {"input_image"};
	const char *out_names[1] = {"output_image"};
	const void *inputs[1] = {input};
	void *output = NULL;
	err = ort_take_error(table, run_fn(sess, NULL, in_names, inputs, 1, out_names, 1, &output));
	release_value(input);
	if (err) {
		return err;
	}
	void *values = NULL;
	err = ort_take_error(table, tensor_data(output, &values));
	if (!err && values) {
		memcpy(output_copy, values, output_elems * sizeof(float));
	}
	release_value(output);
	return err;
}

static void ort_close_all(void *handle, const void *const *table, void *sess, void *info, void *env) {
	if (table) {
		if (sess) {
			((ort_release_fn)table[95])(sess);
		}
		if (info) {
			((ort_release_fn)table[94])(info);
		}
		if (env) {
			((ort_release_fn)table[92])(env);
		}
	}
	if (handle) {
		dlclose(handle);
	}
}
*/
import "C"

import (
	"fmt"
	"math"
	"unsafe"
)

const (
	apiVersion    = 24
	warningsOnly  = 2 // ORT_LOGGING_LEVEL_WARNING
	deviceMemory  = 0 // OrtDeviceAllocator
	defaultMemory = 0 // OrtMemTypeDefault
)

type ort struct {
	handle unsafe.Pointer
	table  *unsafe.Pointer
	coreml unsafe.Pointer
	env    unsafe.Pointer
	info   unsafe.Pointer
	sess   unsafe.Pointer
	logits []float32
}

func cErr(err *C.char) error {
	if err == nil {
		return nil
	}
	defer C.free(unsafe.Pointer(err))
	return fmt.Errorf("ONNX Runtime: %s", C.GoString(err))
}

// load starts ONNX Runtime from library via dlopen and reads the model: using CoreML on Apple Silicon when that works,
// and falling back to CPU otherwise.
func load(library, model string) (network, error) {
	cLib := C.CString(library)
	defer C.free(unsafe.Pointer(cLib))

	var handle, coreml unsafe.Pointer
	var table *unsafe.Pointer
	var errMsg *C.char
	rc := C.ort_open_lib(cLib, C.uint32_t(apiVersion), &handle, &table, &coreml, &errMsg)
	if rc == -1 {
		return nil, fmt.Errorf("this ONNX Runtime has no version %d of its interface", apiVersion)
	}
	if rc == 0 {
		defer C.free(unsafe.Pointer(errMsg))
		return nil, fmt.Errorf("loading ONNX Runtime: %s", C.GoString(errMsg))
	}
	o := &ort{handle: handle, table: table, coreml: coreml, logits: make([]float32, Side*Side)}

	cName := C.CString("forge")
	defer C.free(unsafe.Pointer(cName))
	if err := cErr(C.ort_create_env(o.table, warningsOnly, cName, &o.env)); err != nil {
		o.Close()
		return nil, err
	}
	if err := cErr(C.ort_create_cpu_info(o.table, deviceMemory, defaultMemory, &o.info)); err != nil {
		o.Close()
		return nil, err
	}

	cModel := C.CString(model)
	defer C.free(unsafe.Pointer(cModel))
	if o.coreml != nil {
		if err := cErr(C.ort_create_session(o.table, o.coreml, o.env, cModel, 1, &o.sess)); err == nil {
			return o, nil
		}
	}
	if err := cErr(C.ort_create_session(o.table, nil, o.env, cModel, 0, &o.sess)); err != nil {
		o.Close()
		return nil, err
	}
	return o, nil
}

// Run looks at a picture and returns Side×Side alpha values.
func (o *ort) Run(rgb []byte) ([]byte, error) {
	data := tensor(rgb)
	shape := [4]C.int64_t{1, 3, Side, Side}
	if err := cErr(C.ort_run_matte(o.table, o.info, o.sess,
		(*C.float)(unsafe.Pointer(&data[0])), C.size_t(len(data)*4),
		&shape[0], 4,
		(*C.float)(unsafe.Pointer(&o.logits[0])), C.size_t(len(o.logits)))); err != nil {
		return nil, err
	}
	out := make([]byte, Side*Side)
	for i, v := range o.logits {
		p := 1 / (1 + math.Exp(-float64(v)))
		out[i] = uint8(p*255 + 0.5)
	}
	return out, nil
}

// Close releases the session, memory info, environment and loaded library.
func (o *ort) Close() {
	if o.handle == nil && o.table == nil {
		return
	}
	C.ort_close_all(o.handle, o.table, o.sess, o.info, o.env)
	o.handle, o.table, o.coreml, o.sess, o.info, o.env = nil, nil, nil, nil, nil, nil
}
