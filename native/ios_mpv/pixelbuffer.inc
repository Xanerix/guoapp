// Included by vo_libmpv.c in the GuoApp iOS libmpv build.
#include <CoreVideo/CoreVideo.h>
#include <libavutil/hwcontext.h>

#include "video/hwdec.h"
#include "video/mp_image_pool.h"
#include "video/sws_utils.h"

struct guoapp_pixelbuffer_frame {
    CVPixelBufferRef buffer;
    double pts;
};

struct guoapp_pixelbuffer_output {
    struct mp_hwdec_ctx device;
    struct mp_sws_context *scaler;
    struct mp_image_pool *download_pool;
    CVPixelBufferPoolRef buffer_pool;
    int width, height;
    int frame_path;
};

static int guoapp_pixelbuffer_init(struct render_backend *ctx,
                                   mpv_render_param *params)
{
    const char *api = get_mpv_render_param(params, MPV_RENDER_PARAM_API_TYPE, "");
    if (strcmp(api, "guoapp-ios-pixelbuffer") != 0)
        return MPV_ERROR_NOT_IMPLEMENTED;

    struct guoapp_pixelbuffer_output *output =
        talloc_zero(ctx, struct guoapp_pixelbuffer_output);
    ctx->priv = output;
    output->scaler = mp_sws_alloc(output);
    output->download_pool = mp_image_pool_new(output);
    output->device.driver_name = "videotoolbox";
    output->device.hw_imgfmt = IMGFMT_VIDEOTOOLBOX;
    if (av_hwdevice_ctx_create(&output->device.av_device_ref,
                               AV_HWDEVICE_TYPE_VIDEOTOOLBOX, NULL, NULL, 0) < 0)
        return MPV_ERROR_UNSUPPORTED;

    ctx->hwdec_devs = hwdec_devices_create();
    hwdec_devices_add(ctx->hwdec_devs, &output->device);
    return 0;
}

static bool guoapp_pixelbuffer_check_format(struct render_backend *ctx, int imgfmt)
{
    struct guoapp_pixelbuffer_output *output = ctx->priv;
    return imgfmt == IMGFMT_VIDEOTOOLBOX ||
           mp_sws_supports_formats(output->scaler, IMGFMT_BGRA, imgfmt);
}

static int guoapp_pixelbuffer_target_size(struct render_backend *ctx,
                                         mpv_render_param *params, int *w, int *h)
{
    *w = *h = 1;
    return 0;
}

static CVPixelBufferRef guoapp_compatible_buffer(
    struct guoapp_pixelbuffer_output *output, struct mp_image *image)
{
    if (output->width != image->w || output->height != image->h) {
        if (output->buffer_pool)
            CVPixelBufferPoolRelease(output->buffer_pool);
        output->buffer_pool = NULL;
        output->width = image->w;
        output->height = image->h;
    }
    if (!output->buffer_pool) {
        int format = kCVPixelFormatType_32BGRA;
        CFNumberRef w = CFNumberCreate(NULL, kCFNumberIntType, &output->width);
        CFNumberRef h = CFNumberCreate(NULL, kCFNumberIntType, &output->height);
        CFNumberRef f = CFNumberCreate(NULL, kCFNumberIntType, &format);
        CFDictionaryRef surface = CFDictionaryCreate(NULL, NULL, NULL, 0,
            &kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
        const void *keys[] = {kCVPixelBufferWidthKey, kCVPixelBufferHeightKey,
            kCVPixelBufferPixelFormatTypeKey, kCVPixelBufferIOSurfacePropertiesKey,
            kCVPixelBufferMetalCompatibilityKey};
        const void *values[] = {w, h, f, surface, kCFBooleanTrue};
        CFDictionaryRef attrs = CFDictionaryCreate(NULL, keys, values, 5,
            &kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
        CVPixelBufferPoolCreate(NULL, NULL, attrs, &output->buffer_pool);
        CFRelease(attrs);
        CFRelease(surface);
        CFRelease(f);
        CFRelease(h);
        CFRelease(w);
    }
    if (!output->buffer_pool)
        return NULL;

    int max_buffers = 8;
    CFNumberRef limit = CFNumberCreate(NULL, kCFNumberIntType, &max_buffers);
    const void *key = kCVPixelBufferPoolAllocationThresholdKey;
    const void *value = limit;
    CFDictionaryRef options = CFDictionaryCreate(NULL, &key, &value, 1,
        &kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
    CVPixelBufferRef buffer = NULL;
    CVReturn status = CVPixelBufferPoolCreatePixelBufferWithAuxAttributes(
        NULL, output->buffer_pool, options, &buffer);
    CFRelease(options);
    CFRelease(limit);
    if (status != kCVReturnSuccess)
        return NULL;

    struct mp_image *download = NULL;
    if (image->imgfmt == IMGFMT_VIDEOTOOLBOX) {
        download = mp_image_hw_download(image, output->download_pool);
        image = download;
    }
    bool ok = false;
    if (image && CVPixelBufferLockBaseAddress(buffer, 0) == kCVReturnSuccess) {
        struct mp_image target = {0};
        struct mp_image_params params = {
            .imgfmt = IMGFMT_BGRA, .w = output->width, .h = output->height,
        };
        mp_image_params_guess_csp(&params);
        mp_image_set_params(&target, &params);
        target.planes[0] = CVPixelBufferGetBaseAddress(buffer);
        target.stride[0] = CVPixelBufferGetBytesPerRow(buffer);
        ok = mp_sws_scale(output->scaler, &target, image) >= 0;
        CVPixelBufferUnlockBaseAddress(buffer, 0);
    }
    talloc_free(download);
    if (ok)
        return buffer;
    CVPixelBufferRelease(buffer);
    return NULL;
}

static int guoapp_pixelbuffer_render(struct render_backend *ctx,
                                     mpv_render_param *params, struct vo_frame *frame)
{
    // Private parameter. A successful call transfers one retain to the caller.
    struct guoapp_pixelbuffer_frame *result = get_mpv_render_param(params, 1000, NULL);
    if (!result)
        return MPV_ERROR_INVALID_PARAMETER;

    *result = (struct guoapp_pixelbuffer_frame){0};
    struct mp_image *image = frame->current;
    if (!image)
        return 0;
    struct guoapp_pixelbuffer_output *output = ctx->priv;
    CVPixelBufferRef native = image->imgfmt == IMGFMT_VIDEOTOOLBOX ?
        (CVPixelBufferRef)image->planes[3] : NULL;
    OSType format = native ? CVPixelBufferGetPixelFormatType(native) : 0;
    bool direct = format == kCVPixelFormatType_420YpCbCr8BiPlanarVideoRange ||
                  format == kCVPixelFormatType_420YpCbCr8BiPlanarFullRange ||
                  format == kCVPixelFormatType_32BGRA;
    result->buffer = direct ? CVPixelBufferRetain(native) :
        guoapp_compatible_buffer(output, image);
    result->pts = image->pts;
    int path = direct ? 1 : 2;
    if (result->buffer && output->frame_path != path) {
        output->frame_path = path;
        MP_INFO(ctx, "VideoToolbox pixel-buffer output: %s at %.3f s\n",
                direct ? "direct" : "compatibility", result->pts);
    }
    return 0;
}

static void guoapp_pixelbuffer_destroy(struct render_backend *ctx)
{
    struct guoapp_pixelbuffer_output *output = ctx->priv;
    if (!output)
        return;
    if (ctx->hwdec_devs) {
        hwdec_devices_remove(ctx->hwdec_devs, &output->device);
        hwdec_devices_destroy(ctx->hwdec_devs);
    }
    av_buffer_unref(&output->device.av_device_ref);
    if (output->buffer_pool)
        CVPixelBufferPoolRelease(output->buffer_pool);
}

static void guoapp_pixelbuffer_reconfig(struct render_backend *ctx,
                                       struct mp_image_params *params) {}
static void guoapp_pixelbuffer_reset(struct render_backend *ctx) {}
static void guoapp_pixelbuffer_external(struct render_backend *ctx, struct vo *vo) {}
static void guoapp_pixelbuffer_resize(struct render_backend *ctx, struct mp_rect *src,
                                      struct mp_rect *dst, struct mp_osd_res *osd) {}

static const struct render_backend_fns guoapp_pixelbuffer_backend = {
    .init = guoapp_pixelbuffer_init,
    .check_format = guoapp_pixelbuffer_check_format,
    .reconfig = guoapp_pixelbuffer_reconfig,
    .reset = guoapp_pixelbuffer_reset,
    .update_external = guoapp_pixelbuffer_external,
    .resize = guoapp_pixelbuffer_resize,
    .get_target_size = guoapp_pixelbuffer_target_size,
    .render = guoapp_pixelbuffer_render,
    .destroy = guoapp_pixelbuffer_destroy,
};
