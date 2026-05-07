import sys


def fail_fast_if_cuda_requested_without_gpu(device: str) -> None:
    if device != "cuda":
        return
    try:
        import torch
    except ImportError:
        sys.stderr.write(
            "torch not installed; cannot probe for CUDA. Install torch or set DEVICE=cpu.\n"
        )
        sys.exit(1)
    if not torch.cuda.is_available():
        sys.stderr.write(
            "cuda device requested but no GPU detected; "
            "set DEVICE=cpu to fall back to CPU runtime\n"
        )
        sys.exit(1)
