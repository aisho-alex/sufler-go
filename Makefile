BINARY := bin/sufler
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
BUILD_FLAGS := -trimpath -ldflags "-s -w -X main.version=$(VERSION)"
WHISPER_DIR := third_party/whisper.cpp
export CGO_CFLAGS = -I$(CURDIR)/$(WHISPER_DIR)/include -I$(CURDIR)/$(WHISPER_DIR)/ggml/include
export CGO_LDFLAGS = -L$(CURDIR)/lib -lggml-cuda -lcublas -lcublasLt -lcudart -lcuda

.PHONY: build check test whisper-build model clean

build:
	go build $(BUILD_FLAGS) -o $(BINARY) ./cmd/sufler

check: build
	./$(BINARY) --check

test:
	go test ./... -count=1

whisper-build:
	cmake -S $(WHISPER_DIR) -B $(WHISPER_DIR)/build \
		-DGGML_CUDA=1 -DBUILD_SHARED_LIBS=OFF \
		-DCMAKE_CUDA_ARCHITECTURES=89 -DGGML_NATIVE=ON \
		-DWHISPER_BUILD_TESTS=OFF -DWHISPER_BUILD_EXAMPLES=OFF \
		-DCMAKE_BUILD_TYPE=Release
	cmake --build $(WHISPER_DIR)/build --config Release -j$(shell nproc)
	cp $(WHISPER_DIR)/build/src/libwhisper.a lib/
	cp $(WHISPER_DIR)/build/ggml/src/libggml.a lib/
	cp $(WHISPER_DIR)/build/ggml/src/libggml-base.a lib/
	cp $(WHISPER_DIR)/build/ggml/src/libggml-cpu.a lib/
	cp $(WHISPER_DIR)/build/ggml/src/ggml-cuda/libggml-cuda.a lib/

model:
	mkdir -p models
	curl -4 -L -C - -o models/ggml-large-v3-turbo.bin.part \
		https://huggingface.co/ggerganov/whisper.cpp/resolve/main/ggml-large-v3-turbo.bin
	mv models/ggml-large-v3-turbo.bin.part models/ggml-large-v3-turbo.bin

clean:
	rm -rf bin
