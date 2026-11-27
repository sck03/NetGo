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
    private let stateLock = NSLock()
    private var generation: UInt64 = 0
    private var active = false
    private func current(_ value: UInt64) -> Bool {stateLock.lock();defer{stateLock.unlock()};return active && generation==value}
    func start() {stateLock.lock();generation &+= 1;active=true;stateLock.unlock();DispatchQueue.main.async { [weak self] in self?.layer.flushAndRemoveImage() }}

    func video(_ bytes: Data, time: Int64) -> Bool {
        stateLock.lock();let version=generation;let enabled=active;stateLock.unlock()
        guard enabled, bytes.count <= 4*1024*1024 else{return false}
        // The native worker supplies backpressure; at most one frame enters here.
        return queue.sync { guard current(version) else{return false};return decode(bytes,time:time,version:version) }
    }
    private func decode(_ bytes: Data, time: Int64, version: UInt64) -> Bool {
        return bytes.withUnsafeBytes { (data: UnsafeRawBufferPointer) in
            decodeBuffer(data, time: time, version: version)
        }
    }
    private func decodeBuffer(_ data: UnsafeRawBufferPointer, time: Int64, version: UInt64) -> Bool {
        var starts: [(Int,Int)] = [], index = 0
        while index + 3 < data.count {
            if data[index] == 0 && data[index+1] == 0 && data[index+2] == 1 { starts.append((index,index+3)); index += 3 }
            else if data[index] == 0 && data[index+1] == 0 && data[index+2] == 0 && data[index+3] == 1 { starts.append((index,index+4)); index += 4 }
            else { index += 1 }
        }
        guard !starts.isEmpty else { return false }
        var frame = Data(capacity: data.count + starts.count * 4)
        for n in starts.indices {
            let end = n + 1 < starts.count ? starts[n+1].0 : data.count
            let start = starts[n].1
            guard start < end else { continue }
            let type = data[start] & 31
            if type == 7 || type == 8 {
                let nal = Data(bytes: data.baseAddress!.advanced(by: start), count: end - start)
                if type == 7 { if sps != nal { sps = nal; format = nil } }
                else if pps != nal { pps = nal; format = nil }
                continue
            }
            var size = UInt32(end - start).bigEndian
            withUnsafeBytes(of: &size) { frame.append(contentsOf: $0) }
            frame.append(data.baseAddress!.advanced(by: start).assumingMemoryBound(to: UInt8.self), count: end - start)
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
        if frame.isEmpty {return true}
        guard let format = format else { return false }
        var block: CMBlockBuffer?, sample: CMSampleBuffer?
        guard CMBlockBufferCreateWithMemoryBlock(allocator: kCFAllocatorDefault, memoryBlock: nil, blockLength: frame.count, blockAllocator: kCFAllocatorDefault, customBlockSource: nil, offsetToData: 0, dataLength: frame.count, flags: 0, blockBufferOut: &block) == noErr, let block = block else { return false }
        frame.withUnsafeBytes { _ = CMBlockBufferReplaceDataBytes(with: $0.baseAddress!, blockBuffer: block, offsetIntoDestination: 0, dataLength: frame.count) }
        var timing = CMSampleTimingInfo(duration: .invalid, presentationTimeStamp: CMTime(value: time,timescale: 10_000_000), decodeTimeStamp: .invalid)
        var size = frame.count
        guard CMSampleBufferCreateReady(allocator: kCFAllocatorDefault, dataBuffer: block, formatDescription: format, sampleCount: 1, sampleTimingEntryCount: 1, sampleTimingArray: &timing, sampleSizeEntryCount: 1, sampleSizeArray: &size, sampleBufferOut: &sample) == noErr, let sample = sample else { return false }
        if let attachments = CMSampleBufferGetSampleAttachmentsArray(sample,createIfNecessary: true), let first = (attachments as NSArray).firstObject as? NSMutableDictionary { first[kCMSampleAttachmentKey_DisplayImmediately] = true }
        // Keep the native worker bounded while transient display pressure clears.
        // Retain this reference frame; dropping compressed frames breaks decoding.
        for _ in 0..<10 {
            let result: Int = DispatchQueue.main.sync {
                guard current(version), layer.status != .failed else { return -1 }
                guard layer.isReadyForMoreMediaData else { return 0 }
                layer.enqueue(sample)
                return 1
            }
            if result != 0 { return result > 0 }
            guard current(version) else { return false }
            Thread.sleep(forTimeInterval: 0.005)
        }
        return false
    }

    func audio(_ bytes: Data) -> Bool {
        stateLock.lock();let version=generation;stateLock.unlock()
        return queue.sync {
            guard current(version), bytes.count > 0, bytes.count % 4 == 0, bytes.count <= 3840 else { return false }
            let format = AVAudioFormat(commonFormat: .pcmFormatFloat32,sampleRate: 48000,channels: 2,interleaved: false)!
            if self.audioEngine == nil {
                let engine = AVAudioEngine(), node = AVAudioPlayerNode()
                engine.attach(node); engine.connect(node,to: engine.mainMixerNode,format: format)
                do { try engine.start(); node.play(); self.audioEngine = engine; self.audioNode = node } catch { return false }
            }
            guard self.pendingAudio < 8, let buffer = AVAudioPCMBuffer(pcmFormat: format,frameCapacity: AVAudioFrameCount(bytes.count / 4)) else { return false }
            buffer.frameLength = buffer.frameCapacity
            bytes.withUnsafeBytes { raw in
                let samples = raw.bindMemory(to: Int16.self)
                for frame in 0..<Int(buffer.frameLength) { for channel in 0..<2 { buffer.floatChannelData![channel][frame] = Float(Int16(littleEndian: samples[frame*2+channel])) / 32768 } }
            }
            self.pendingAudio += 1
            self.audioNode?.scheduleBuffer(buffer) { [weak self] in self?.queue.async { guard let self=self,self.current(version) else{return};self.pendingAudio=max(0,self.pendingAudio-1) } }
            return true
        }
    }
    func stop() {
        stateLock.lock();generation &+= 1;let version=generation;active=false;stateLock.unlock()
        queue.async { [weak self] in
            guard let self=self else{return}
            self.audioNode?.stop(); self.audioEngine?.stop(); self.audioEngine=nil;self.audioNode=nil;self.pendingAudio=0;self.format=nil;self.sps=Data();self.pps=Data()
        }
        DispatchQueue.main.async { [weak self] in
            guard let self=self else{return};self.stateLock.lock();defer{self.stateLock.unlock()};if self.generation==version {self.layer.flushAndRemoveImage()}
        }
    }
}
