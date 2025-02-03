
(() => { // UPLOAD
  const upload_input = document.getElementById("upload-input");
  const uploadInfoContainer = document.querySelector(".upload-info-container");
  const uploadInfoFilename = document.querySelector(".upload-info-container .file-name");
  const uploadInfoFileno = document.querySelector(".upload-info-container .file-no");
  const uploadInfoProgress = document.querySelector(".upload-info-container .progress-bar");
  const uploadInfoSpeed = document.querySelector(".upload-info-container .speed");
  const uploadInfoTime = document.querySelector(".upload-info-container .time");
  let uploadProcessed = false;

  function setUploadProgress(percent, from=0, to=1) {
    percent = from + percent * (to - from);
    uploadInfoProgress.style.width = `${Math.floor(percent * 100)}%`;
  }

  function updateThroughput(totalUploaded, totalSize, elapsed) {

    const elapsedSeconds = elapsed / 1000;
    const speed = totalUploaded / elapsedSeconds;
    uploadInfoSpeed.textContent = formatBytes(speed) + "/s";

    const left = Math.max(1, Math.floor((elapsedSeconds/totalUploaded) * (totalSize - totalUploaded)));

    const mins = Math.floor(left / 60);
    const secs = left % 60;
    uploadInfoTime.textContent = `${String(mins).padStart(2, '0')}:${String(secs).padStart(2, '0')}`;

  }

  upload_input.addEventListener("change", function() {
    if(uploadProcessed) return;
    if(this.files.length == 0) return;

    uploadFiles([...this.files]);
  }, false);

  window.uploadFiles = async function (files) {

    mainWrap.scrollTo({ top: 0, behavior: 'smooth' });

    uploadProcessed = true;
    uploadInfoContainer.classList.remove("error");
    uploadInfoContainer.style.display = '';

    const totalSize = files.reduce((acc, file) => acc + file.size, 0);

    try {

      for (let i = 0; i < files.length; i++) {

        const file = files[i];

        uploadInfoFilename.textContent = file.name;
        uploadInfoFileno.textContent = `${i+1} of ${files.length}`;

        await uploadSingleFile(file);
      
      }

      //uploadInfoContainer.style.display = 'none';
      uploadInfoSpeed.textContent = formatBytes(totalSize) + " uploaded";
      uploadInfoTime.textContent = "-";

    } catch(error) {

      checkServer();
      uploadInfoContainer.classList.add("error");
      uploadInfoSpeed.textContent = error.message;
      return;

    }

    uploadProcessed = false;
    await populateList(false);
    await initializeAudio();

  }

  function uploadSingleFile(file) {
    
    return new Promise(async (resolve, reject) => {

      let progressFrom = 0;
      const makeProgressCallback = (progressFragment) => {
        return (message, percent) => {
          uploadInfoSpeed.textContent = message;
          uploadInfoTime.textContent = "-";
          setUploadProgress(percent, progressFrom, progressFrom + progressFragment);
          if (percent === 1) {
            progressFrom += progressFragment;
          }
        };
      };
      setUploadProgress(0);

      // Preprocessing
      const cat = file.type.split("/", 2)[0];
      if (cat === "audio") {
        file = await ffmpegSoundCheck.call(file).track(makeProgressCallback(0.15));
      }

      //
      const arrayBuffer = await file.arrayBuffer(); // Read the file as an ArrayBuffer
      const crc = await hashwasm.crc32(new Uint8Array(arrayBuffer));
  
      // Create FormData
      const formData = new FormData();
      formData.append('crc', crc);
      formData.append('file', file);

      // Check for sub metadata
      const metaBlobMap = await ffmpegAttemptMetadata(file);
      for (let ext in metaBlobMap) {
        formData.append(`metadata:${ext}`, metaBlobMap[ext]);
      }

      // Fake endpoint - replace with your actual server upload URL
      const uploadUrl = buildURL("/upload", {[QUERY_ALBUM]: gAlbum});

      // Setup XHR
      const xhr = new XMLHttpRequest();
      xhr.open('POST', uploadUrl, true);
      const start = Date.now();

      // Track upload progress
      xhr.upload.onprogress = (event) => {
        if (event.lengthComputable) {
          setUploadProgress(event.loaded / event.total, progressFrom);

          // Send delta back
          updateThroughput(event.loaded, event.total, Date.now()-start);
        }
      };

      // On load (finished)
      xhr.onload = () => {
        if (xhr.status === 200) {
          // File uploaded successfully
          resolve();
        } else {
          // Handle error
          reject(new Error(xhr.status + ": " + xhr.response));
        }
        checkServer();
      };

      // On error
      xhr.onerror = () => {
        reject(new Error('Network error'));
      };

      // Send the file
      xhr.send(formData);
    });
  }

  // UPLOAD DRAG HANDLING
  
  // Helper states
  const dragIndicator = document.querySelector(".drag-indicator");
  let dragCounter = 0; // track how many "dragenter" events
  let dragStartedInternal = false; // to feign knowing it came from inside
                  // simply leaving and reentering would make it seem from outside

  function isDragFromOutside(event) {
    
    for (const item of event.dataTransfer.items) {
      if (item.type === "text/uri-list") {
        const url = event.dataTransfer.getData(item.type);
        try {
          // Parse the URL to extract its origin
          const parsedUrl = new URL(url);
          if (parsedUrl.origin === window.location.origin)
            return false;
        } catch (error) {
          console.log(`Invalid URL: ${url}`);
        }
      }
    }

    return true;

  }

  function handleDragStart(event) {
    dragStartedInternal = true;
  }

  function handleDragEnter(event) {
    if(uploadProcessed) return;

    event.preventDefault();
    event.stopPropagation();

    dragCounter++;

    if (dragStartedInternal) return;

    /* dragstart and drop has access to drop info
    if (isDragFromOutside(event) === false)
      return;
    */

    dragIndicator.style.display = '';
  }

  function handleDragOver(event) {
    event.preventDefault();
    event.stopPropagation();
  }

  function handleDragLeave(event) {
    event.preventDefault();
    event.stopPropagation();
    dragCounter--;

    // If we are not in a nested drag area, hide the overlay
    if (dragCounter === 0) {
      dragIndicator.style.display = 'none';
      dragStartedInternal = false;
    }
  }

  function handleDrop(event) {
    if(uploadProcessed) return;

    event.preventDefault();
    event.stopPropagation();
    dragCounter = 0;
    dragStartedInternal = false;

    // Hide the overlay
    dragIndicator.style.display = 'none';

    const files = event.dataTransfer.files;
    if (files.length === 0) return;

    if (isDragFromOutside(event) === false)
      return;

    // Upload each file in series or parallel (example: in series for clarity)
    uploadFiles([...files]);
  }

  // Set up document-level event listeners for drag and drop
  document.addEventListener('dragstart', handleDragStart);
  document.addEventListener('dragenter', handleDragEnter);
  document.addEventListener('dragover', handleDragOver);
  document.addEventListener('dragleave', handleDragLeave);
  document.addEventListener('drop', handleDrop);


})();