#include "cursor_surface.hpp"

namespace netgo::capture {
HRESULT CursorSurface::compose(ID3D11Device* device, ID3D11DeviceContext* context,
                               ID3D11Texture2D* source, const RECT& desktop, bool separate,
                               ID3D11Texture2D** output) noexcept {
    *output = source;
    CURSORINFO cursor{};
    cursor.cbSize = sizeof(cursor);
    if (!separate || !GetCursorInfo(&cursor) || !(cursor.flags & CURSOR_SHOWING)) return S_OK;
    if (cursor.hCursor != cursor_) {
        ICONINFO icon{};
        if (!GetIconInfo(cursor.hCursor, &icon)) return S_OK;
        hotspot_ = {LONG(icon.xHotspot), LONG(icon.yHotspot)};
        if (icon.hbmColor) DeleteObject(icon.hbmColor);
        if (icon.hbmMask) DeleteObject(icon.hbmMask);
        cursor_ = cursor.hCursor;
    }
    D3D11_TEXTURE2D_DESC description{};
    source->GetDesc(&description);
    if (texture_) {
        D3D11_TEXTURE2D_DESC current{};
        texture_->GetDesc(&current);
        if (current.Width != description.Width || current.Height != description.Height) texture_.Reset();
    }
    if (!texture_) {
        description.Usage = D3D11_USAGE_DEFAULT;
        description.BindFlags = D3D11_BIND_RENDER_TARGET;
        description.CPUAccessFlags = 0;
        description.MiscFlags = D3D11_RESOURCE_MISC_GDI_COMPATIBLE;
        const auto result = device->CreateTexture2D(&description, nullptr, texture_.GetAddressOf());
        if (FAILED(result)) return result;
    }
    context->CopyResource(texture_.Get(), source);
    Microsoft::WRL::ComPtr<IDXGISurface1> surface;
    auto result = texture_.As(&surface);
    if (FAILED(result)) return result;
    HDC dc = nullptr;
    result = surface->GetDC(FALSE, &dc);
    if (FAILED(result)) return result;
    const bool drawn = DrawIconEx(dc, cursor.ptScreenPos.x - desktop.left - hotspot_.x,
                                  cursor.ptScreenPos.y - desktop.top - hotspot_.y,
                                  cursor.hCursor, 0, 0, 0, nullptr, DI_NORMAL) != FALSE;
    result = surface->ReleaseDC(nullptr);
    if (FAILED(result)) return result;
    if (!drawn) return E_FAIL;
    *output = texture_.Get();
    return S_OK;
}
}
