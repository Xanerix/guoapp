// packages/media_kit_video/windows/d3d11_surface_manager.cc
#include "d3d11_surface_manager.h"

#include <iostream>
#include <stdexcept>
#include <vector>

#define FAIL(message)                                                    \
  std::cout << "media_kit: D3D11SurfaceManager: " << message << std::endl; \
  return false
#define CHECK_HRESULT(message) \
  if (FAILED(hr)) { FAIL(message); }

D3D11SurfaceManager::D3D11SurfaceManager(int32_t width, int32_t height)
    : width_(width), height_(height) {
  mutex_ = ::CreateMutex(NULL, FALSE, NULL);
  Create();
}

D3D11SurfaceManager::~D3D11SurfaceManager() {
  CleanUp(true);
  ::CloseHandle(mutex_);
}

void D3D11SurfaceManager::SetSize(int32_t width, int32_t height) {
  if (width == width_ && height == height_) return;
  width_ = width;
  height_ = height;
  Create();
}

void D3D11SurfaceManager::Draw(std::function<void()> callback) {
  ::WaitForSingleObject(mutex_, INFINITE);
  callback();
  d3d_11_device_context_->Flush();
  ::ReleaseMutex(mutex_);
}

void D3D11SurfaceManager::Read() {
  ::WaitForSingleObject(mutex_, INFINITE);
  if (d3d_11_device_context_ != nullptr) {
    d3d_11_device_context_->CopyResource(d3d_11_texture_2D_.Get(),
                                         internal_d3d_11_texture_2D_.Get());
    d3d_11_device_context_->Flush();
  }
  ::ReleaseMutex(mutex_);
}

void D3D11SurfaceManager::Create() {
  CleanUp(false);
  if (!CreateD3DTexture() || handle_ == nullptr) {
    throw std::runtime_error(
        "Unable to create Direct3D 11 surface for mpv D3D11 render API.");
  }
}

bool D3D11SurfaceManager::CreateD3DTexture() {
  if (d3d_11_device_ == nullptr) {
    const std::vector<D3D_FEATURE_LEVEL> feature_levels = {
        D3D_FEATURE_LEVEL_11_0, D3D_FEATURE_LEVEL_10_1,
        D3D_FEATURE_LEVEL_10_0, D3D_FEATURE_LEVEL_9_3,
    };
    IDXGIAdapter* adapter = nullptr;
    D3D_DRIVER_TYPE driver_type = D3D_DRIVER_TYPE_UNKNOWN;
    if (Utils::IsWindows10RTMOrGreater()) {
      adapter = NULL;
      driver_type = D3D_DRIVER_TYPE_HARDWARE;
    } else {
      IDXGIFactory* dxgi = nullptr;
      ::CreateDXGIFactory(__uuidof(IDXGIFactory), (void**)&dxgi);
      dxgi->EnumAdapters(0, &adapter);
      dxgi->Release();
    }

    auto hr = ::D3D11CreateDevice(
        adapter, driver_type, 0, 0, feature_levels.data(),
        static_cast<UINT>(feature_levels.size()), D3D11_SDK_VERSION,
        &d3d_11_device_, 0, &d3d_11_device_context_);
    CHECK_HRESULT("D3D11CreateDevice");
  }

  Microsoft::WRL::ComPtr<IDXGIDevice> dxgi_device = nullptr;
  auto dxgi_device_success = d3d_11_device_->QueryInterface(
      __uuidof(IDXGIDevice), (void**)&dxgi_device);
  if (SUCCEEDED(dxgi_device_success) && dxgi_device != nullptr) {
    dxgi_device->SetGPUThreadPriority(5);  // Must be in interval [-7, 7].
  }

  auto level = d3d_11_device_->GetFeatureLevel();
  std::cout << "media_kit: D3D11SurfaceManager: Direct3D Feature Level: "
            << (((unsigned)level) >> 12) << "_"
            << ((((unsigned)level) >> 8) & 0xf) << std::endl;

  auto desc = D3D11_TEXTURE2D_DESC{0};
  desc.Width = width_;
  desc.Height = height_;
  desc.Format = DXGI_FORMAT_B8G8R8A8_UNORM;
  desc.MipLevels = 1;
  desc.ArraySize = 1;
  desc.SampleDesc.Count = 1;
  desc.SampleDesc.Quality = 0;
  desc.Usage = D3D11_USAGE_DEFAULT;
  desc.BindFlags = D3D11_BIND_RENDER_TARGET | D3D11_BIND_SHADER_RESOURCE;
  desc.CPUAccessFlags = 0;

  desc.MiscFlags = 0;
  auto hr = d3d_11_device_->CreateTexture2D(&desc, nullptr,
                                            &internal_d3d_11_texture_2D_);
  CHECK_HRESULT("ID3D11Device::CreateTexture2D(internal)");

  desc.MiscFlags = D3D11_RESOURCE_MISC_SHARED;
  hr = d3d_11_device_->CreateTexture2D(&desc, nullptr,
                                       &d3d_11_texture_2D_);
  CHECK_HRESULT("ID3D11Device::CreateTexture2D(external)");

  Microsoft::WRL::ComPtr<IDXGIResource> resource;
  hr = d3d_11_texture_2D_.As(&resource);
  CHECK_HRESULT("ID3D11Texture2D::As(IDXGIResource)");
  hr = resource->GetSharedHandle(&handle_);
  CHECK_HRESULT("IDXGIResource::GetSharedHandle");
  return true;
}

void D3D11SurfaceManager::CleanUp(bool release_device) {
  internal_d3d_11_texture_2D_.Reset();
  d3d_11_texture_2D_.Reset();
  handle_ = nullptr;

  if (release_device) {
    d3d_11_device_context_.Reset();
    d3d_11_device_.Reset();
  }
}
