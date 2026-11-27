#pragma once
#include <d3d11.h>
#include <dxgi1_2.h>
#include <wrl/client.h>

namespace netgo::capture {
// Capture-thread-owned composition texture. GDI handles color, alpha and
// monochrome cursor masks without a full desktop readback into application RAM.
class CursorSurface {
    Microsoft::WRL::ComPtr<ID3D11Texture2D> texture_;
    HCURSOR cursor_ = nullptr;
    POINT hotspot_{};
public:
    void reset() { texture_.Reset(); cursor_ = nullptr; }
    HRESULT compose(ID3D11Device* device, ID3D11DeviceContext* context,
                    ID3D11Texture2D* source, const RECT& desktop, bool separate,
                    ID3D11Texture2D** output) noexcept;
};
}
