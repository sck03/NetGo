import Foundation
import AVFoundation
import CoreMedia

final class MediaPlayer {
    let layer = AVSampleBufferDisplayLayer()
    private let queue = DispatchQueue(label: "org.netgo.media")
    private var format: CMVideoFormatDescription?
    private var sps = Data(), pps = Data()
    private var audioEngine: AVAudioEngine?
    private var audioNode: AVAudioPlayerNode?
    private var pendingAudio = 0

    func video(_ bytes: Data, time: Int64) {
        queue.async { [weak self] in self?.decode(bytes, time: time) }
    }
    private func decode(_ bytes: Data, time: Int64) {
        let data = [UInt8](bytes)
        var starts: [(Int,Int)] = [], index = 0
        while index + 3 < data.count {
            if data[index] == 0 && data[index+1] == 0 && data[index+2] == 1 { starts.append((index,index+3)); index += 3 }
            else if data[index] == 0 && data[index+1] == 0 && data[index+2] == 0 && data[index+3] == 1 { starts.append((index,index+4)); index += 4 }
            else { index += 1 }
        }
        guard !starts.isEmpty else { return }
        var frame = Data()
        for n in starts.indices {
            let end = n + 1 < starts.count ? starts[n+1].0 : data.count
            let nal = Data(data[starts[n].1..<end]); guard let first = nal.first else { continue }
            if first & 31 == 7 { if sps != nal { sps = nal; format = nil }; continue }
            if first & 31 == 8 { if pps != nal { pps = nal; format = nil }; continue }
            var size = UInt32(nal.count).bigEndian
            withUnsafeBytes(of: &size) { frame.append(contentsOf: $0) }; frame.append(nal)
        }
        if format == nil && !sps.isEmpty && !pps.isEmpty {
            sps.withUnsafeBytes { a in pps.withUnsafeBytes { b in
                let pointers = [a.baseAddress!.assumingMemoryBound(to: UInt8.self), b.baseAddress!.assumingMemoryBound(to: UInt8.self)]
                let sizes = [sps.count,pps.count]
                pointers.withUnsafeBufferPointer { p in sizes.withUnsafeBufferPointer { s in
                    _ = CMVideoFormatDescriptionCreateFromH264ParameterSets(allocator: kCFAllocatorDefault, parameterSetCount: 2, parameterSetPointers: p.baseAddress!, parameterSetSizes: s.baseAddress!, nalUnitHeaderLength: 4, formatDescriptionOut: &format)
                } }
            } }
        }
        guard let format = format, !frame.isEmpty else { return }
        var block: CMBlockBuffer?, sample: CMSampleBuffer?
        guard CMBlockBufferCreateWithMemoryBlock(allocator: kCFAllocatorDefault, memoryBlock: nil, blockLength: frame.count, blockAllocator: kCFAllocatorDefault, customBlockSource: nil, offsetToData: 0, dataLength: frame.count, flags: 0, blockBufferOut: &block) == noErr, let block = block else { return }
        frame.withUnsafeBytes { _ = CMBlockBufferReplaceDataBytes(with: $0.baseAddress!, blockBuffer: block, offsetIntoDestination: 0, dataLength: frame.count) }
        var timing = CMSampleTimingInfo(duration: .invalid, presentationTimeStamp: CMTime(value: time,timescale: 10_000_000), decodeTimeStamp: .invalid)
        var size = frame.count
        guard CMSampleBufferCreateReady(allocator: kCFAllocatorDefault, dataBuffer: block, formatDescription: format, sampleCount: 1, sampleTimingEntryCount: 1, sampleTimingArray: &timing, sampleSizeEntryCount: 1, sampleSizeArray: &size, sampleBufferOut: &sample) == noErr, let sample = sample else { return }
        if let attachments = CMSampleBufferGetSampleAttachmentsArray(sample,createIfNecessary: true), let first = (attachments as NSArray).firstObject as? NSMutableDictionary { first[kCMSampleAttachmentKey_DisplayImmediately] = true }
        DispatchQueue.main.async { [weak self] in
            guard let self = self else { return }
            if self.layer.status == .failed { self.layer.flush() }
            if self.layer.isReadyForMoreMediaData { self.layer.enqueue(sample) }
        }
    }
    func audio(_ bytes: Data) {
        queue.async { [weak self] in
            guard let self = self, bytes.count % 4 == 0, bytes.count <= 3840 else { return }
            let format = AVAudioFormat(commonFormat: .pcmFormatFloat32,sampleRate: 48000,channels: 2,interleaved: false)!
            if self.audioEngine == nil {
                let engine = AVAudioEngine(), node = AVAudioPlayerNode()
                engine.attach(node); engine.connect(node,to: engine.mainMixerNode,format: format)
                do { try engine.start(); node.play(); self.audioEngine = engine; self.audioNode = node } catch { return }
            }
            guard self.pendingAudio < 8, let buffer = AVAudioPCMBuffer(pcmFormat: format,frameCapacity: AVAudioFrameCount(bytes.count / 4)) else { return }
            buffer.frameLength = buffer.frameCapacity
            bytes.withUnsafeBytes { raw in
                let samples = raw.bindMemory(to: Int16.self)
                for frame in 0..<Int(buffer.frameLength) { for channel in 0..<2 { buffer.floatChannelData![channel][frame] = Float(Int16(littleEndian: samples[frame*2+channel])) / 32768 } }
            }
            self.pendingAudio += 1
            self.audioNode?.scheduleBuffer(buffer) { [weak self] in self?.queue.async { self?.pendingAudio = max(0,(self?.pendingAudio ?? 1)-1) } }
        }
    }
    func stop() { queue.async { [weak self] in self?.audioNode?.stop(); self?.audioEngine?.stop(); self?.audioEngine = nil; self?.audioNode = nil; self?.format = nil }; DispatchQueue.main.async { [weak self] in self?.layer.flushAndRemoveImage() } }
}
