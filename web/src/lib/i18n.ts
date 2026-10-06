import i18n from "i18next";
import { initReactI18next } from "react-i18next";
import LanguageDetector from "i18next-browser-languagedetector";

import en from "../locales/en.json";
import it from "../locales/it.json";
import fr from "../locales/fr.json";
import de from "../locales/de.json";
import es from "../locales/es.json";
import pt from "../locales/pt.json";

export const LANGUAGES = [
  { code: "en", label: "English" },
  { code: "it", label: "Italiano" },
  { code: "fr", label: "Français" },
  { code: "de", label: "Deutsch" },
  { code: "es", label: "Español" },
  { code: "pt", label: "Português" },
];

export const LANGUAGE_CODES = LANGUAGES.map((l) => l.code);

i18n
  .use(LanguageDetector)
  .use(initReactI18next)
  .init({
    resources: {
      en: { translation: en },
      it: { translation: it },
      fr: { translation: fr },
      de: { translation: de },
      es: { translation: es },
      pt: { translation: pt },
    },
    fallbackLng: "en",
    supportedLngs: LANGUAGE_CODES,
    load: "languageOnly",
    detection: {
      order: ["localStorage", "navigator"],
      lookupLocalStorage: "stiva-lang",
      caches: ["localStorage"],
    },
    interpolation: { escapeValue: false },
  });

i18n.on("languageChanged", (lng) => {
  document.documentElement.lang = lng;
});
document.documentElement.lang = i18n.language?.split("-")[0] || "en";

export default i18n;
