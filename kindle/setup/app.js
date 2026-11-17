/*
  Audiobookshelf Sync setup screen. Runs in the Kindle's web app engine (mesquite), an old WebKit:
  plain ES5 and XMLHttpRequest only. Talks to the abs-sync service on 127.0.0.1.
*/
var API = "http://127.0.0.1:47823/api/";
var APP_ID = "io.github.abskindlesync";

function $(id) { return document.getElementById(id); }

function show(section) {
  var ids = ["signin", "settings", "offline"];
  for (var i = 0; i < ids.length; i++) {
    $(ids[i]).style.display = ids[i] === section ? "block" : "none";
  }
}

function message(text) {
  var m = $("message");
  m.style.display = text ? "block" : "none";
  m.textContent = text || "";
}

function call(method, path, body, done) {
  var xhr = new XMLHttpRequest();
  xhr.open(method, API + path, true);
  xhr.timeout = 60000;
  xhr.setRequestHeader("Content-Type", "application/json");
  xhr.onreadystatechange = function() {
    if (xhr.readyState !== 4) return;
    var data = null;
    try { data = JSON.parse(xhr.responseText); } catch (e) {}
    if (xhr.status === 0) return done("offline", null);
    if (xhr.status >= 400) return done((data && data.error) || ("Error " + xhr.status), null);
    done(null, data);
  };
  xhr.send(body ? JSON.stringify(body) : null);
}

function when(ms) {
  if (!ms) return "never";
  var mins = Math.round((Date.now() - ms) / 60000);
  if (mins < 1) return "just now";
  if (mins < 60) return mins + " min ago";
  var hours = Math.round(mins / 60);
  if (hours < 48) return hours + " h ago";
  return Math.round(hours / 24) + " days ago";
}

function renderStatus(st) {
  $("account").textContent = st.username + " on " + st.server;
  var text = st.books + (st.books === 1 ? " book" : " books") + " on this Kindle";
  if (st.syncing) text += " · syncing…";
  else text += " · last sync " + when(st.last_sync) + (st.last_result ? " (" + st.last_result + ")" : "");
  if (st.last_error) text += " · " + st.last_error;
  $("sync-status").textContent = text;
  $("ask").checked = !!st.ask_before_jump;
}

function renderLibraries(libs) {
  var list = $("libraries");
  while (list.firstChild) list.removeChild(list.firstChild);
  for (var i = 0; i < libs.length; i++) {
    var label = document.createElement("label");
    var box = document.createElement("input");
    box.type = "checkbox";
    box.value = libs[i].id;
    box.checked = libs[i].selected;
    box.onchange = saveLibraries;
    label.appendChild(box);
    label.appendChild(document.createTextNode(libs[i].name));
    list.appendChild(label);
  }
}

function saveLibraries() {
  var boxes = $("libraries").getElementsByTagName("input");
  var ids = [];
  for (var i = 0; i < boxes.length; i++) if (boxes[i].checked) ids.push(boxes[i].value);
  if (!ids.length) { message("Pick at least one library."); return; }
  message("");
  call("POST", "libraries", { ids: ids }, function(err, data) {
    if (err) return message(err);
    renderLibraries(data.libraries);
    refresh();
  });
}

function refresh() {
  call("GET", "status", null, function(err, st) {
    if (err === "offline") return show("offline");
    if (err) return message(err);
    $("version").textContent = st.version ? "Audiobookshelf Sync " + st.version : "";
    if (!st.signed_in) {
      show("signin");
      if (st.server) $("server").value = st.server;
      if (st.username) $("username").value = st.username;
      return;
    }
    show("settings");
    renderStatus(st);
    if (st.syncing) setTimeout(refresh, 3000);
  });
}

function loadLibraries() {
  call("GET", "libraries", null, function(err, data) {
    if (!err) renderLibraries(data.libraries);
  });
}

$("signin-button").onclick = function() {
  var button = this;
  button.disabled = true;
  button.textContent = "Signing in…";
  message("");
  call("POST", "login", {
    server: $("server").value,
    username: $("username").value,
    password: $("password").value
  }, function(err, data) {
    button.disabled = false;
    button.textContent = "Sign in";
    $("password").value = "";
    if (err) return message(err === "offline" ? "Audiobookshelf Sync isn't running." : err);
    renderLibraries(data.libraries);
    call("POST", "sync", {}, function() { refresh(); });
  });
};

$("sync-button").onclick = function() {
  message("");
  call("POST", "sync", {}, function(err, st) {
    if (err) return message(err);
    renderStatus(st);
    setTimeout(refresh, 2000);
  });
};

$("ask").onchange = function() {
  call("POST", "settings", { ask_before_jump: this.checked }, function(err) {
    if (err) message(err);
  });
};

$("signout-button").onclick = function() {
  call("POST", "logout", {}, function(err) {
    if (err) return message(err);
    refresh();
  });
};

function configureChrome() {
  try {
    window.kindle.messaging.sendMessage("com.lab126.chromebar", "configureChrome", {
      appId: APP_ID,
      topNavBar: {
        template: "title",
        title: "Audiobookshelf Sync",
        buttons: [{ id: "KPP_CLOSE", state: "enabled", handling: "system" }]
      }
    });
  } catch (e) {}
}

if (window.kindle && window.kindle.appmgr) {
  window.kindle.appmgr.ongo = function() { configureChrome(); refresh(); };
}
refresh();
loadLibraries();
