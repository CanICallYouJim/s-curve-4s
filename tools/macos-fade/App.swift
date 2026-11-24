import AppKit

private let ink = NSColor(calibratedRed: 21/255, green: 35/255, blue: 45/255, alpha: 1)
private let mint = NSColor(calibratedRed: 99/255, green: 215/255, blue: 193/255, alpha: 1)
private let muted = NSColor(calibratedRed: 145/255, green: 168/255, blue: 170/255, alpha: 1)

func selectedDirectory(_ urls: [URL]) -> URL? {
    guard urls.count == 1, let url = urls.first, url.isFileURL else { return nil }
    var isDirectory: ObjCBool = false
    guard FileManager.default.fileExists(atPath: url.path, isDirectory: &isDirectory), isDirectory.boolValue else { return nil }
    return url.standardizedFileURL
}

func droppedDirectory(_ info: NSDraggingInfo) -> URL? {
    let urls = info.draggingPasteboard.readObjects(forClasses: [NSURL.self], options: [.urlReadingFileURLsOnly: true]) as? [URL] ?? []
    return selectedDirectory(urls)
}

final class DropView: NSView {
    var accept: ((URL) -> Void)?
    var canDrop: (() -> Bool)?
    override init(frame: NSRect) {
        super.init(frame: frame)
        registerForDraggedTypes([.fileURL])
        wantsLayer = true
        layer?.backgroundColor = ink.cgColor
    }
    required init?(coder: NSCoder) { fatalError("init(coder:)") }
    override func draggingEntered(_ sender: NSDraggingInfo) -> NSDragOperation {
        canDrop?() == true && droppedDirectory(sender) != nil ? .copy : []
    }
    override func draggingUpdated(_ sender: NSDraggingInfo) -> NSDragOperation { draggingEntered(sender) }
    override func performDragOperation(_ sender: NSDraggingInfo) -> Bool {
        guard canDrop?() == true, let folder = droppedDirectory(sender) else { return false }
        accept?(folder)
        return true
    }
}

final class FolderBox: NSComboBox {
    var accept: ((URL) -> Void)?
    var canDrop: (() -> Bool)?
    override func draggingEntered(_ sender: NSDraggingInfo) -> NSDragOperation {
        canDrop?() == true && droppedDirectory(sender) != nil ? .copy : []
    }
    override func draggingUpdated(_ sender: NSDraggingInfo) -> NSDragOperation { draggingEntered(sender) }
    override func performDragOperation(_ sender: NSDraggingInfo) -> Bool {
        guard canDrop?() == true, let folder = droppedDirectory(sender) else { return false }
        accept?(folder)
        return true
    }
}

final class BrandView: NSView {
    private let sprite: NSImage?
    private var tracking: NSTrackingArea?
    private var timer: Timer?
    private var start: TimeInterval = 0
    private var frameIndex = 0
    init(frame: NSRect, resource: URL?) {
        sprite = resource.flatMap { NSImage(contentsOf: $0) }
        super.init(frame: frame)
        if let sprite { sprite.size = NSSize(width: 2880, height: 1521) }
        setAccessibilityLabel("by IG")
    }
    required init?(coder: NSCoder) { fatalError("init(coder:)") }
    override func updateTrackingAreas() {
        super.updateTrackingAreas()
        if let tracking { removeTrackingArea(tracking) }
        tracking = NSTrackingArea(rect: bounds, options: [.mouseEnteredAndExited, .activeInKeyWindow, .inVisibleRect], owner: self)
        if let tracking { addTrackingArea(tracking) }
    }
    override func mouseEntered(with event: NSEvent) {
        guard !NSWorkspace.shared.accessibilityDisplayShouldReduceMotion, sprite != nil else { return }
        reset()
        start = ProcessInfo.processInfo.systemUptime
        let timer = Timer(timeInterval: 1/60, repeats: true) { [weak self] _ in
            guard let self else { return }
            let elapsed = ProcessInfo.processInfo.systemUptime - self.start
            if elapsed >= 4.5 { self.reset(); return }
            self.frameIndex = min(134, Int(elapsed * 30))
            self.needsDisplay = true
        }
        self.timer = timer
        RunLoop.main.add(timer, forMode: .common)
    }
    override func mouseExited(with event: NSEvent) { reset() }
    private func reset() { timer?.invalidate(); timer = nil; frameIndex = 0; needsDisplay = true }
    override func viewDidMoveToWindow() {
        super.viewDidMoveToWindow()
        if window == nil { reset() }
    }
    override func draw(_ dirtyRect: NSRect) {
        ("by" as NSString).draw(at: NSPoint(x: 0, y: 24), withAttributes: [.font: NSFont.systemFont(ofSize: 11), .foregroundColor: muted])
        guard let sprite else { return }
        let height = 48.0 * 169 / 192
        let source = NSRect(x: (frameIndex % 15) * 192, y: (8 - frameIndex / 15) * 169, width: 192, height: 169)
        sprite.draw(in: NSRect(x: 22, y: (62-height)/2, width: 48, height: height), from: source, operation: .sourceOver, fraction: 1, respectFlipped: false, hints: [.interpolation: NSImageInterpolation.high.rawValue])
    }
    deinit { timer?.invalidate() }
}

func userFacingError(_ message: String) -> String {
    if message.contains("operation not permitted") || message.contains("permission denied") {
        return "Нет доступа к папке. Выберите её через «Обзор…» и повторите."
    }
    return message
}

struct EngineResult: Decodable {
    let output: String
    let done: Int
    let failed: Int
    let error: String
}

struct UpdateStatus: Decodable {
    let current: String
    let latest: String
    let available: Bool
    let notes: String
    let error: String
    let target: String
}

extension UpdateStatus {
    private enum CodingKeys: String, CodingKey { case current, latest, available, notes, error, target }
    init(from decoder: Decoder) throws {
        let values = try decoder.container(keyedBy: CodingKeys.self)
        current = try values.decodeIfPresent(String.self, forKey: .current) ?? ""
        latest = try values.decodeIfPresent(String.self, forKey: .latest) ?? ""
        available = try values.decodeIfPresent(Bool.self, forKey: .available) ?? false
        notes = try values.decodeIfPresent(String.self, forKey: .notes) ?? ""
        error = try values.decodeIfPresent(String.self, forKey: .error) ?? ""
        target = try values.decodeIfPresent(String.self, forKey: .target) ?? ""
    }
}

struct UpdateInstallResult: Decodable {
    let target: String
    let error: String
}

func versionBanner(_ update: UpdateStatus, fallback: String) -> String {
    let current = update.current.isEmpty ? fallback : update.current
    if update.available { return "Версия \(current) · Доступно обновление \(update.latest)" }
    if !update.target.isEmpty { return "Версия \(current) · Обновление готово к запуску" }
    if !update.error.isEmpty { return "Версия \(current) · Не удалось проверить обновления" }
    return "Версия \(current) · Установлена последняя версия"
}

final class AppDelegate: NSObject, NSApplicationDelegate, NSWindowDelegate {
    private var window: NSWindow!
    private let folders = FolderBox(frame: NSRect(x: 28, y: 350, width: 486, height: 28))
    private var browse: NSButton!
    private var start: NSButton!
    private var stop: NSButton!
    private var reveal: NSButton!
    private var status: NSTextField!
    private var versionLabel: NSTextField!
    private var updateBanner: NSView!
    private var updateButton: NSButton!
    private var checkProcess: Process?
    private var updateProcess: Process?
    private var updateAvailable = false
    private var cachedTarget = ""
    private var openingUpdate = false
    private var currentVersion: String { Bundle.main.object(forInfoDictionaryKey: "CFBundleShortVersionString") as? String ?? "1.2.0" }
    private let log = NSTextView()
    private var logBytes = Data()
    private var process: Process?
    private var output: URL?
    private var quitAfterStop = false
    private var pendingFolder: URL?
    private var busy: Bool { process != nil || updateProcess != nil || openingUpdate }
    private var historyURL: URL {
        FileManager.default.urls(for: .applicationSupportDirectory, in: .userDomainMask)[0]
            .appendingPathComponent("LenaDJ/SCurve/folders.json")
    }
    func applicationDidFinishLaunching(_ notification: Notification) {
        buildWindow()
        refreshHistory()
        if let index = CommandLine.arguments.firstIndex(of: "--folder"), index + 1 < CommandLine.arguments.count,
           let folder = selectedDirectory([URL(fileURLWithPath: CommandLine.arguments[index + 1])]) { pendingFolder = folder }
        if let folder = pendingFolder { setFolder(folder); pendingFolder = nil }
        window.makeKeyAndOrderFront(nil)
        NSApp.activate(ignoringOtherApps: true)
        checkForUpdates()
    }
    private func label(_ text: String, _ frame: NSRect, size: CGFloat = 13, color: NSColor = .white, bold: Bool = false) -> NSTextField {
        let field = NSTextField(labelWithString: text)
        field.frame = frame
        field.font = bold ? .boldSystemFont(ofSize: size) : .systemFont(ofSize: size)
        field.textColor = color
        window.contentView?.addSubview(field)
        return field
    }
    private func button(_ title: String, _ frame: NSRect, _ action: Selector) -> NSButton {
        let button = NSButton(title: title, target: self, action: action)
        button.frame = frame
        button.bezelStyle = .rounded
        window.contentView?.addSubview(button)
        return button
    }
    private func buildWindow() {
        window = NSWindow(contentRect: NSRect(x: 0, y: 0, width: 660, height: 520), styleMask: [.titled, .closable, .miniaturizable], backing: .buffered, defer: false)
        window.title = "S-кривая · 4 секунды"
        window.delegate = self
        window.isReleasedWhenClosed = false
        window.appearance = NSAppearance(named: .darkAqua)
        let root = DropView(frame: NSRect(x: 0, y: 0, width: 660, height: 520))
        root.canDrop = { [weak self] in self?.busy == false }
        root.accept = { [weak self] in self?.setFolder($0) }
        window.contentView = root
        updateBanner = NSView(frame: NSRect(x: 20, y: 470, width: 620, height: 38))
        updateBanner.wantsLayer = true
        updateBanner.layer?.cornerRadius = 8
        updateBanner.layer?.backgroundColor = mint.withAlphaComponent(0.08).cgColor
        updateBanner.layer?.borderColor = mint.withAlphaComponent(0.35).cgColor
        updateBanner.layer?.borderWidth = 1
        updateBanner.isHidden = true
        root.addSubview(updateBanner)
        versionLabel = label("Версия \(currentVersion) · Проверяем обновления…", NSRect(x: 28, y: 478, width: 450, height: 24), size: 12, color: muted)
        versionLabel.lineBreakMode = .byTruncatingTail
        updateButton = button("Обновить", NSRect(x: 490, y: 474, width: 142, height: 30), #selector(installUpdate))
        updateButton.isHidden = true
        updateButton.isEnabled = false
        _ = label("Плавное начало музыки", NSRect(x: 28, y: 415, width: 604, height: 36), size: 26, bold: true)
        _ = label("Перетащите папку в окно или выберите из шести последних", NSRect(x: 28, y: 388, width: 604, height: 22), color: muted)
        folders.numberOfVisibleItems = 6
        folders.isEditable = true
        folders.completes = false
        folders.registerForDraggedTypes([.fileURL])
        folders.setAccessibilityLabel("Папка с музыкой")
        folders.canDrop = { [weak self] in self?.busy == false }
        folders.accept = { [weak self] in self?.setFolder($0) }
        root.addSubview(folders)
        browse = button("Обзор…", NSRect(x: 526, y: 348, width: 106, height: 32), #selector(chooseFolder))
        _ = label("Только треки этой папки. Результаты — в «S-кривая 4 с».", NSRect(x: 28, y: 317, width: 604, height: 24), color: muted)
        status = label("Выберите папку и нажмите «Обработать».", NSRect(x: 28, y: 282, width: 604, height: 24))
        status.lineBreakMode = .byTruncatingMiddle
        let scroll = NSScrollView(frame: NSRect(x: 28, y: 112, width: 604, height: 160))
        scroll.hasVerticalScroller = true
        scroll.borderType = .noBorder
        scroll.wantsLayer = true
        scroll.layer?.cornerRadius = 8
        log.frame = NSRect(origin: .zero, size: scroll.contentSize)
        log.minSize = NSSize(width: 0, height: scroll.contentSize.height)
        log.maxSize = NSSize(width: CGFloat.greatestFiniteMagnitude, height: CGFloat.greatestFiniteMagnitude)
        log.isEditable = false
        log.isSelectable = true
        log.isRichText = false
        log.font = .monospacedSystemFont(ofSize: 11, weight: .regular)
        log.textColor = NSColor(calibratedWhite: 0.88, alpha: 1)
        log.backgroundColor = NSColor(calibratedWhite: 0.06, alpha: 1)
        log.textContainerInset = NSSize(width: 10, height: 10)
        log.autoresizingMask = [.width]
        log.isVerticallyResizable = true
        log.isHorizontallyResizable = false
        log.textContainer?.widthTracksTextView = true
        scroll.documentView = log
        root.addSubview(scroll)
        let brand = BrandView(frame: NSRect(x: 28, y: 22, width: 112, height: 62), resource: Bundle.main.url(forResource: "brand", withExtension: "png"))
        root.addSubview(brand)
        reveal = button("Результаты", NSRect(x: 252, y: 38, width: 110, height: 32), #selector(showOutput))
        reveal.isEnabled = false
        stop = button("Остановить", NSRect(x: 366, y: 38, width: 116, height: 32), #selector(stopProcessing))
        stop.isEnabled = false
        start = button("Обработать", NSRect(x: 490, y: 38, width: 142, height: 32), #selector(startProcessing))
        start.contentTintColor = mint
        start.keyEquivalent = "\r"
        window.center()
        let menu = NSMenu()
        let appItem = NSMenuItem()
        menu.addItem(appItem)
        let appMenu = NSMenu()
        appMenu.addItem(withTitle: "Завершить S-кривую", action: #selector(NSApplication.terminate(_:)), keyEquivalent: "q")
        appItem.submenu = appMenu
        let editItem = NSMenuItem(title: "Правка", action: nil, keyEquivalent: "")
        menu.addItem(editItem)
        let editMenu = NSMenu(title: "Правка")
        editMenu.addItem(withTitle: "Копировать", action: #selector(NSText.copy(_:)), keyEquivalent: "c")
        editMenu.addItem(withTitle: "Вставить", action: #selector(NSText.paste(_:)), keyEquivalent: "v")
        editMenu.addItem(withTitle: "Выделить всё", action: #selector(NSText.selectAll(_:)), keyEquivalent: "a")
        editItem.submenu = editMenu
        NSApp.mainMenu = menu
    }
    private func refreshHistory() {
        let prior = folders.stringValue
        let history = (try? Data(contentsOf: historyURL)).flatMap { try? JSONDecoder().decode([String].self, from: $0) } ?? []
        folders.removeAllItems()
        folders.addItems(withObjectValues: Array(history.prefix(6)))
        folders.stringValue = prior.isEmpty ? (history.first ?? FileManager.default.homeDirectoryForCurrentUser.appendingPathComponent("Music").path) : prior
    }
    private func setFolder(_ url: URL) {
        guard !busy else { return }
        folders.stringValue = url.path
        status?.stringValue = "Папка выбрана. Нажмите «Обработать»."
    }
    @objc private func chooseFolder() {
        guard !busy else { return }
        let panel = NSOpenPanel()
        panel.canChooseFiles = false
        panel.canChooseDirectories = true
        panel.allowsMultipleSelection = false
        panel.prompt = "Выбрать"
        panel.directoryURL = selectedDirectory([URL(fileURLWithPath: folders.stringValue)])
        panel.beginSheetModal(for: window) { [weak self] response in
            if response == .OK, let folder = selectedDirectory(panel.urls) { self?.setFolder(folder) }
        }
    }
    @objc private func startProcessing() {
        guard !busy else { return }
        let path = (folders.stringValue.trimmingCharacters(in: .whitespacesAndNewlines) as NSString).expandingTildeInPath
        guard !path.isEmpty, let folder = selectedDirectory([URL(fileURLWithPath: path)]) else {
            status.stringValue = "Выберите существующую папку."
            return
        }
        guard let resources = Bundle.main.resourceURL, let executable = Bundle.main.executableURL else { return }
        let temporary = FileManager.default.temporaryDirectory.appendingPathComponent("ig-fade-" + UUID().uuidString, isDirectory: true)
        do { try FileManager.default.createDirectory(at: temporary, withIntermediateDirectories: true) }
        catch { status.stringValue = "Не удалось подготовить запуск: \(error.localizedDescription)"; return }
        let resultURL = temporary.appendingPathComponent("result.json")
        let task = Process()
        task.executableURL = executable.deletingLastPathComponent().appendingPathComponent("fade-engine")
        task.arguments = ["--folder", folder.path, "--tools", resources.appendingPathComponent("tools").path, "--result", resultURL.path]
        let pipe = Pipe()
        task.standardOutput = pipe
        task.standardError = pipe
        task.standardInput = FileHandle.nullDevice
        logBytes.removeAll()
        log.string = ""
        output = nil
        reveal.isEnabled = false
        folders.stringValue = folder.path
        do { try task.run() }
        catch {
            try? FileManager.default.removeItem(at: temporary)
            status.stringValue = "Не удалось запустить обработку: \(error.localizedDescription)"
            return
        }
        process = task
        setBusy(true)
        refreshHistory()
        status.stringValue = "Обработка… Исходные треки остаются на месте."
        DispatchQueue.global(qos: .userInitiated).async { [weak self] in
            while true {
                let data = pipe.fileHandleForReading.availableData
                if data.isEmpty { break }
                DispatchQueue.main.async { [weak self] in self?.appendLog(data) }
            }
            task.waitUntilExit()
            let result = (try? Data(contentsOf: resultURL)).flatMap { try? JSONDecoder().decode(EngineResult.self, from: $0) }
            try? FileManager.default.removeItem(at: temporary)
            DispatchQueue.main.async { [weak self] in self?.finished(result, exitCode: task.terminationStatus) }
        }
    }
    private func appendLog(_ data: Data) {
        logBytes.append(data)
        if logBytes.count > 262_144 { logBytes.removeFirst(logBytes.count - 262_144) }
        log.string = String(decoding: logBytes, as: UTF8.self)
        log.scrollToEndOfDocument(nil)
    }
    private func setBusy(_ value: Bool) {
        folders.isEnabled = !value
        browse.isEnabled = !value
        start.isEnabled = !value
        stop.isEnabled = value && (process != nil || updateProcess != nil)
        updateButton.isEnabled = !value && updateAvailable
    }
    private func finished(_ result: EngineResult?, exitCode: Int32) {
        process = nil
        setBusy(false)
        refreshHistory()
        if let result {
            if !result.output.isEmpty { output = URL(fileURLWithPath: result.output) }
            if exitCode == 2 { status.stringValue = "Остановлено. Готово: \(result.done). Ошибок: \(result.failed)." }
            else if !result.error.isEmpty { status.stringValue = userFacingError(result.error) }
            else { status.stringValue = "Готово: \(result.done). Ошибок: \(result.failed)." }
        } else { status.stringValue = "Обработка завершилась с кодом \(exitCode). Подробности — в журнале." }
        reveal.isEnabled = output != nil
        if quitAfterStop { NSApp.reply(toApplicationShouldTerminate: true) }
    }
    @objc private func showOutput() { if let output { NSWorkspace.shared.open(output) } }
    @objc private func stopProcessing() {
        if let updateProcess, updateProcess.isRunning {
            stop.isEnabled = false
            status.stringValue = "Отменяем загрузку обновления…"
            updateProcess.interrupt()
            return
        }
        guard let process, process.isRunning else { return }
        stop.isEnabled = false
        status.stringValue = "Останавливаем обработку…"
        process.interrupt()
    }
    // The updater runs separately from audio conversion. Checking availability
    // never blocks the window, and installation cannot start while music is busy.
    private func launchUpdateCommand(_ mode: String, completion: @escaping (Data?, Int32) -> Void) throws -> Process {
        guard let executable = Bundle.main.executableURL else {
            throw NSError(domain: "IGUpdater", code: 1, userInfo: [NSLocalizedDescriptionKey: "Не найден движок обновления."])
        }
        let temporary = FileManager.default.temporaryDirectory.appendingPathComponent("ig-update-" + UUID().uuidString, isDirectory: true)
        try FileManager.default.createDirectory(at: temporary, withIntermediateDirectories: true)
        let resultURL = temporary.appendingPathComponent("result.json")
        let task = Process()
        task.executableURL = executable.deletingLastPathComponent().appendingPathComponent("fade-engine")
        task.arguments = [mode, "--result", resultURL.path]
        task.standardOutput = FileHandle.nullDevice
        task.standardError = FileHandle.nullDevice
        task.standardInput = FileHandle.nullDevice
        do { try task.run() }
        catch { try? FileManager.default.removeItem(at: temporary); throw error }
        DispatchQueue.global(qos: .utility).async {
            task.waitUntilExit()
            let data = try? Data(contentsOf: resultURL)
            try? FileManager.default.removeItem(at: temporary)
            DispatchQueue.main.async { completion(data, task.terminationStatus) }
        }
        return task
    }
    private func checkForUpdates() {
        do {
            checkProcess = try launchUpdateCommand("--update-status") { [weak self] data, _ in
                guard let self else { return }
                self.checkProcess = nil
                guard let data, let update = try? JSONDecoder().decode(UpdateStatus.self, from: data) else {
                    self.versionLabel.stringValue = "Версия \(self.currentVersion) · Не удалось проверить обновления"
                    return
                }
                self.versionLabel.stringValue = versionBanner(update, fallback: self.currentVersion)
                self.versionLabel.toolTip = update.error.isEmpty ? update.notes : update.error
                self.cachedTarget = update.target
                self.updateAvailable = update.available || !update.target.isEmpty
                self.updateBanner.isHidden = !self.updateAvailable
                self.versionLabel.textColor = self.updateAvailable ? mint : muted
                self.updateButton.isHidden = !self.updateAvailable
                self.updateButton.isEnabled = self.updateAvailable && !self.busy
                self.updateButton.title = update.target.isEmpty ? "Обновить" : "Перезапустить"
                if !update.target.isEmpty && !self.busy { self.openUpdatedApp(update.target) }
            }
        } catch { versionLabel.stringValue = "Версия \(currentVersion) · Не удалось проверить обновления" }
    }
    @objc private func installUpdate() {
        guard !busy, updateAvailable else { return }
        if !cachedTarget.isEmpty { openUpdatedApp(cachedTarget); return }
        do {
            updateProcess = try launchUpdateCommand("--install-update") { [weak self] data, exitCode in
                guard let self else { return }
                self.updateProcess = nil
                self.setBusy(false)
                if self.quitAfterStop { NSApp.reply(toApplicationShouldTerminate: true); return }
                guard let data, let result = try? JSONDecoder().decode(UpdateInstallResult.self, from: data) else {
                    self.status.stringValue = exitCode == 2 ? "Загрузка отменена. Текущая версия сохранена." : "Не удалось установить обновление. Текущая версия сохранена."
                    return
                }
                if !result.error.isEmpty {
                    self.status.stringValue = exitCode == 2 ? "Загрузка отменена. Текущая версия сохранена." : "Обновление не установлено: \(result.error)"
                    return
                }
                guard !result.target.isEmpty else { self.status.stringValue = "Обновление не найдено. Текущая версия сохранена."; return }
                self.cachedTarget = result.target
                self.updateButton.title = "Перезапустить"
                self.openUpdatedApp(result.target)
            }
            setBusy(true)
            status.stringValue = "Загружаем и проверяем обновление…"
        } catch { status.stringValue = "Не удалось начать обновление: \(error.localizedDescription)" }
    }
    private func openUpdatedApp(_ path: String) {
        guard !busy else { return }
        let target = URL(fileURLWithPath: path).standardizedFileURL
        guard target.pathExtension == "app", selectedDirectory([target]) != nil,
              target != Bundle.main.bundleURL.standardizedFileURL else {
            status.stringValue = "Не удалось открыть обновление. Текущая версия доступна."
            return
        }
        openingUpdate = true
        setBusy(true)
        status.stringValue = "Открываем обновлённую версию…"
        let configuration = NSWorkspace.OpenConfiguration()
        configuration.createsNewApplicationInstance = true
        configuration.arguments = ["--folder", folders.stringValue]
        NSWorkspace.shared.openApplication(at: target, configuration: configuration) { [weak self] _, error in
            DispatchQueue.main.async {
                guard let self else { return }
                self.openingUpdate = false
                if let error {
                    self.setBusy(false)
                    self.status.stringValue = "Не удалось открыть обновление: \(error.localizedDescription)"
                } else { NSApp.terminate(nil) }
            }
        }
    }
    func application(_ sender: NSApplication, openFiles filenames: [String]) {
        guard !busy, let folder = selectedDirectory(filenames.map { URL(fileURLWithPath: $0) }) else {
            sender.reply(toOpenOrPrint: .failure)
            return
        }
        if window == nil { pendingFolder = folder } else { setFolder(folder); window.makeKeyAndOrderFront(nil); NSApp.activate(ignoringOtherApps: true) }
        sender.reply(toOpenOrPrint: .success)
    }
    func windowShouldClose(_ sender: NSWindow) -> Bool {
        NSApp.terminate(nil)
        return false
    }
    func applicationShouldTerminate(_ sender: NSApplication) -> NSApplication.TerminateReply {
        guard !openingUpdate else { return .terminateCancel }
        guard busy else {
            if let checkProcess, checkProcess.isRunning { checkProcess.interrupt(); checkProcess.waitUntilExit() }
            return .terminateNow
        }
        if quitAfterStop { return .terminateLater }
        let alert = NSAlert()
        alert.messageText = updateProcess == nil ? "Остановить обработку и закрыть программу?" : "Отменить обновление и закрыть программу?"
        alert.informativeText = updateProcess == nil ? "Уже готовые файлы останутся в папке результатов. Исходные треки сохранятся." : "Текущая версия останется доступной. Незавершённая загрузка не будет установлена."
        alert.addButton(withTitle: "Остановить и выйти")
        alert.addButton(withTitle: "Продолжить обработку")
        if alert.runModal() != .alertFirstButtonReturn { return .terminateCancel }
        guard busy else { return .terminateNow }
        quitAfterStop = true
        stopProcessing()
        return .terminateLater
    }
}

func selfTest() throws {
    let root = FileManager.default.temporaryDirectory.appendingPathComponent("ig-ui-test-" + UUID().uuidString)
    try FileManager.default.createDirectory(at: root, withIntermediateDirectories: true)
    defer { try? FileManager.default.removeItem(at: root) }
    let folder = root.appendingPathComponent("Папка с пробелами & [1]")
    try FileManager.default.createDirectory(at: folder, withIntermediateDirectories: true)
    let file = root.appendingPathComponent("track.mp3")
    try Data([1]).write(to: file)
    precondition(selectedDirectory([folder]) == folder.standardizedFileURL)
    precondition(selectedDirectory([]) == nil)
    precondition(selectedDirectory([folder, root]) == nil)
    precondition(selectedDirectory([file]) == nil)
    precondition(selectedDirectory([root.appendingPathComponent("missing")]) == nil)
    precondition(selectedDirectory([URL(string: "https://example.com")!]) == nil)
    precondition(userFacingError("open /Users/test/Desktop/music: operation not permitted").contains("Обзор"))
    precondition(userFacingError("permission denied").contains("Обзор"))
    precondition(userFacingError("в этой папке не найдены аудиофайлы") == "в этой папке не найдены аудиофайлы")
    let update = try JSONDecoder().decode(UpdateStatus.self, from: Data("{\"current\":\"1.2.0\",\"latest\":\"1.3.0\",\"available\":true,\"notes\":\"Исправления\",\"error\":\"\",\"target\":\"\"}".utf8))
    precondition(update.available && versionBanner(update, fallback: "1.2.0").contains("1.3.0"))
    let ordinary = try JSONDecoder().decode(UpdateStatus.self, from: Data("{\"current\":\"1.2.0\",\"latest\":\"1.3.0\",\"available\":true,\"notes\":\"Fix\",\"error\":\"\"}".utf8))
    precondition(ordinary.available && ordinary.target.isEmpty)
    let cached = try JSONDecoder().decode(UpdateStatus.self, from: Data("{\"current\":\"1.2.0\",\"target\":\"/tmp/New.app\"}".utf8))
    precondition(cached.target == "/tmp/New.app" && versionBanner(cached, fallback: "1.2.0").contains("готово"))
    let offline = UpdateStatus(current: "1.2.0", latest: "", available: false, notes: "", error: "offline", target: "")
    precondition(versionBanner(offline, fallback: "1.2.0").contains("Не удалось"))
    let installed = try JSONDecoder().decode(UpdateInstallResult.self, from: Data("{\"target\":\"/tmp/S-кривая.app\",\"error\":\"\"}".utf8))
    precondition(installed.target.hasSuffix(".app") && installed.error.isEmpty)
    let result = try JSONDecoder().decode(EngineResult.self, from: Data("{\"output\":\"/tmp/папка\",\"done\":2,\"failed\":1,\"error\":\"\"}".utf8))
    precondition(result.done == 2 && result.failed == 1 && result.output == "/tmp/папка")
    if let path = Bundle.main.url(forResource: "brand", withExtension: "png"), let image = NSImage(contentsOf: path), let rep = image.representations.first {
        precondition(rep.pixelsWide == 192*15 && rep.pixelsHigh == 169*9)
    }
    print("macOS UI self-test passed: one folder, Unicode/spaces, files/multiple/missing rejected; engine/update results decoded; online/offline version banner.")
}

if CommandLine.arguments.contains("--self-test") {
    do { try selfTest() }
    catch { fputs("Self-test failed: \(error)\n", stderr); exit(1) }
} else {
    let app = NSApplication.shared
    let delegate = AppDelegate()
    app.delegate = delegate
    app.setActivationPolicy(.regular)
    app.run()
}
