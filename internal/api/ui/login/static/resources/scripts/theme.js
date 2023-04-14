if (modeOverwriteParam('dark')) {
    document.documentElement.classList.replace('lgn-light-theme', 'lgn-dark-theme');
    writeModeCookie('dark');
} else if (modeOverwriteParam('light')) {
    document.documentElement.classList.replace('lgn-dark-theme', 'lgn-light-theme');
    writeModeCookie('light');
}

function modeOverwriteParam(name) {
    return new URLSearchParams(window.location.search).get('mode') === name;
}

function hasDarkModeOverwriteCookie() {
    return getCookie('mode') === 'dark';
}

function hasLightModeOverwriteCookie() {
    return getCookie('mode') === 'light';
}

function writeModeCookie(mode) {
    setCookie('mode', mode, 365);
}

function getCookie(cname) {
    let name = cname + '=';
    let decodedCookie = decodeURIComponent(document.cookie);
    let ca = decodedCookie.split(';');
    for (let i = 0; i < ca.length; i++) {
        let c = ca[i];
        while (c.charAt(0) === ' ') {
            c = c.substring(1);
        }
        if (c.indexOf(name) === 0) {
            return c.substring(name.length, c.length);
        }
    }
    return '';
}

function setCookie(name, value, exdays) {
    let d = new Date();
    d.setTime(d.getTime() + (exdays * 24 * 60 * 60 * 1000));
    let expires = "expires=" + d.toUTCString();
    document.cookie = name + "=" + value + ";" + expires + ";path=/";
}
