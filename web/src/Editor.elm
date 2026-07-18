module Editor exposing (Effect(..), Model, Msg(..), init, pipeline, update, view)

import Api exposing (Region, Sermon, Timeline, Waveform)
import Html exposing (Html, audio, button, canvas, div, h1, h2, h3, p, span, strong, text)
import Html.Attributes exposing (attribute, class, controls, disabled, id, src, tabindex)
import Html.Events exposing (onClick)
import Http
import Json.Decode as Decode
import Json.Encode as Encode
import Ui

type alias Model = { sermon : Sermon, waveform : Maybe Waveform, analysis : Maybe Timeline, draft : Maybe Timeline, draftReceived : Bool, regions : List Region, selected : Int, dirty : Bool, applying : Bool, finalReady : Bool, confirmingApproval : Bool, error : Maybe String, backWarning : Bool, playhead : Float }
type Msg = GotWaveform (Result Http.Error Waveform) | GotAnalysis (Result Http.Error Timeline) | GotDraft Decode.Value | Select Int | Toggle Int | Nudge Int Float | Boundary Decode.Value | Play Int | Playhead Decode.Value | Apply | Applied (Result Http.Error Sermon) | ConfirmApproval | CancelApproval | Approve | Approved (Result Http.Error Sermon) | Back | ForceClose | KeepEditing
type Effect = None | LoadDraft String | SaveDraft String Encode.Value Encode.Value Encode.Value | ClearDraft String | Render Encode.Value Encode.Value | Preview Encode.Value | PlayRegion Float Float | Close | ApprovedSermon Sermon

init : Sermon -> ( Model, Cmd Msg, Effect )
init sermon =
    ( { sermon = sermon, waveform = Nothing, analysis = Nothing, draft = Nothing, draftReceived = False, regions = [], selected = 0, dirty = False, applying = False, finalReady = sermon.stage == "edit" && sermon.status == "done", confirmingApproval = False, error = Nothing, backWarning = False, playhead = 0 }
    , Cmd.batch [ Api.waveform GotWaveform sermon.id, Api.analyze GotAnalysis sermon.id ]
    , LoadDraft sermon.id )

timelineDecoder : Decode.Decoder Timeline
timelineDecoder = Decode.map2 Timeline (Decode.field "duration" Decode.float) (Decode.field "regions" (Decode.list (Decode.map4 Region (Decode.field "start" Decode.float) (Decode.field "end" Decode.float) (Decode.field "type" Decode.string) (Decode.field "keep" Decode.bool))))

resolve : Model -> Model
resolve model = case ( model.waveform, model.analysis, model.draftReceived ) of
    ( Just wave, Just analyzed, True ) ->
        let chosen = case model.draft of
                Just draft -> if abs (draft.duration - wave.duration) < 0.01 && valid wave.duration draft.regions then draft.regions else analyzed.regions
                Nothing -> analyzed.regions
        in if List.isEmpty model.regions then { model | regions = chosen, dirty = model.draft /= Nothing } else model
    _ -> model

update : Msg -> Model -> ( Model, Cmd Msg, Effect )
update msg model =
    case msg of
        GotWaveform result ->
            case result of
                Ok wave -> finish { model | waveform = Just wave }
                Err _ -> ( { model | error = Just "Could not load the waveform. Please return and try again." }, Cmd.none, None )

        GotAnalysis result ->
            case result of
                Ok value -> finish { model | analysis = Just value }
                Err _ -> ( { model | error = Just "Could not analyze this recording. Please try again." }, Cmd.none, None )

        GotDraft value -> finish { model | draftReceived = True, draft = Decode.decodeValue timelineDecoder value |> Result.toMaybe }
        Select index -> ( { model | selected = index }, Cmd.none, None )
        Toggle index -> changed { model | regions = List.indexedMap (\i r -> if i == index then { r | keep = not r.keep } else r) model.regions, selected = index }
        Nudge boundary delta -> changeBoundary boundary (boundaryTime boundary model.regions + delta) model
        Boundary value ->
            case Decode.decodeValue (Decode.map2 Tuple.pair (Decode.field "boundary" Decode.int) (Decode.field "time" Decode.float)) value of
                Ok ( boundary, time ) -> changeBoundary boundary time model
                Err _ -> ( model, Cmd.none, None )
        Play index ->
            case get index model.regions of
                Just region -> ( { model | selected = index }, Cmd.none, PlayRegion region.start region.end )
                Nothing -> ( model, Cmd.none, None )
        Playhead value ->
            case Decode.decodeValue Decode.float value of
                Ok time -> ( { model | playhead = time }, Cmd.none, None )
                Err _ -> ( model, Cmd.none, None )
        Apply -> ( { model | applying = True, error = Nothing }, Api.applyEdits Applied model.sermon.id model.regions, None )
        Applied result ->
            case result of
                Ok sermon -> ( { model | sermon = sermon, applying = True, dirty = False }, Cmd.none, ClearDraft sermon.id )
                Err _ -> ( { model | applying = False, error = Just "Could not queue the edit. Check the regions and try again." }, Cmd.none, None )
        ConfirmApproval -> ( { model | confirmingApproval = True }, Cmd.none, None )
        CancelApproval -> ( { model | confirmingApproval = False }, Cmd.none, None )
        Approve -> ( { model | applying = True }, Api.approveEdit Approved model.sermon.id, None )
        Approved result ->
            case result of
                Ok sermon -> ( model, Cmd.none, ApprovedSermon sermon )
                Err _ -> ( { model | applying = False, confirmingApproval = False, error = Just "Could not approve the final audio. Please try again." }, Cmd.none, None )
        Back -> if model.dirty then ( { model | backWarning = True }, Cmd.none, None ) else ( model, Cmd.none, Close )
        ForceClose -> ( model, Cmd.none, Close )
        KeepEditing -> ( { model | backWarning = False }, Cmd.none, None )

finish model = let ready = resolve model in ( ready, Cmd.none, if List.isEmpty ready.regions then None else renderEffect ready )
changed model =
    let next = { model | dirty = True, finalReady = False, error = Nothing }
    in case renderEffect next of
        Render render preview -> ( next, Cmd.none, SaveDraft next.sermon.id (encodeTimeline next) render preview )
        _ -> ( next, Cmd.none, SaveDraft next.sermon.id (encodeTimeline next) Encode.null (previewValue next) )
changeBoundary boundary time model =
    let
        before = get (boundary - 1) model.regions
        after = get boundary model.regions
    in case ( before, after ) of
        ( Just left, Just right ) -> changed { model | regions = List.indexedMap (\i r -> if i == boundary - 1 then { r | end = clamp (left.start + 0.01) (right.end - 0.01) time } else if i == boundary then { r | start = clamp (left.start + 0.01) (right.end - 0.01) time } else r) model.regions }
        _ -> ( model, Cmd.none, None )

pipeline sermon model =
    if sermon.id /= model.sermon.id then ( model, None )
    else if sermon.editApproved then ( model, ApprovedSermon sermon )
    else if sermon.stage == "edit" && sermon.status == "done" then let next = { model | sermon = sermon, applying = False, finalReady = True, dirty = False, error = Nothing } in ( next, Preview (previewValue next) )
    else if sermon.stage == "edit" && sermon.status == "failed" then ( { model | sermon = sermon, applying = False, error = sermon.error }, None )
    else ( { model | sermon = sermon, applying = sermon.stage == "edit" && (sermon.status == "pending" || sermon.status == "running") }, None )

valid duration regions = not (List.isEmpty regions) && (List.head regions |> Maybe.map (\r -> abs r.start < 0.01) |> Maybe.withDefault False) && (List.reverse regions |> List.head |> Maybe.map (\r -> abs (r.end-duration) < 0.01) |> Maybe.withDefault False) && List.all (\r -> r.end > r.start) regions
get index list = List.drop index list |> List.head
boundaryTime index regions = get index regions |> Maybe.map .start |> Maybe.withDefault 0
clamp low high value = max low (min high value)
encodeRegions regions = Encode.list (\r -> Encode.object [ ("start",Encode.float r.start),("end",Encode.float r.end),("type",Encode.string r.regionType),("keep",Encode.bool r.keep) ]) regions
encodeTimeline model = Encode.object [ ("duration", Encode.float (model.waveform |> Maybe.map .duration |> Maybe.withDefault 0)), ("regions",encodeRegions model.regions) ]
previewValue model = Encode.object [ ("mode",Encode.string (if model.finalReady then "final" else "preview")), ("regions",encodeRegions model.regions) ]
renderEffect model =
    case model.waveform of
        Just wave ->
            Render (Encode.object [ ( "duration", Encode.float wave.duration ), ( "samples", Encode.list Encode.float wave.samples ), ( "regions", encodeRegions model.regions ), ( "playhead", Encode.float model.playhead ) ]) (previewValue model)

        Nothing ->
            None

view model = div [ class "editor" ] [ h1 [] [ text "Automatic Audio Timeline" ], p [ class "editor__filename" ] [ text model.sermon.originalFilename ], viewBody model ]
viewBody model = if model.waveform == Nothing || model.analysis == Nothing || not model.draftReceived then p [ class "editor__loading" ] [ text "Loading waveform and finding sections…" ] else div [] [ viewError model.error, canvas [ id "timeline-canvas", class "timeline", attribute "role" "img", attribute "aria-label" "Audio waveform with editable regions", tabindex 0 ] [], p [ class "timeline-key" ] [ text "Green: speaking · Red: singing · Gray: silence. Hatched sections will be deleted." ], audio [ id "timeline-audio", class "editor__audio", controls True, src (audioSource model) ] [], viewStatus model, div [ class "editor__actions" ] [ button [ Ui.primaryButton, onClick Apply, disabled (model.applying || model.finalReady || not (validPlan model)) ] [ text (if model.applying then "Rendering…" else "Apply Edits") ], if model.finalReady then button [ Ui.dangerButton, onClick ConfirmApproval, disabled model.applying ] [ text "Approve Final Audio" ] else text "", button [ Ui.button, onClick Back ] [ text "Back to Sermons" ] ], approval model, backWarning model, h2 [] [ text "Sections" ], div [ class "region-list" ] (List.indexedMap (regionCard model) model.regions) ]
validPlan model = valid (model.waveform |> Maybe.map .duration |> Maybe.withDefault 0) model.regions && List.any .keep model.regions
audioSource model = "/api/sermons/" ++ model.sermon.id ++ "/audio/" ++ (if model.finalReady then "final?v=" ++ String.fromInt model.sermon.progress else "proxy?gate=" ++ String.fromInt model.sermon.normalizationGateAdjustment ++ "&volume=" ++ String.fromInt model.sermon.normalizationVolumeAdjustment)
viewStatus model = if model.applying then p [ class "editor__status" ] [ text ("Rendering final audio… " ++ String.fromInt model.sermon.progress ++ "%") ] else if model.finalReady then p [ class "editor__status" ] [ strong [] [ text "Final audio is ready. Listen carefully, then approve or adjust sections and apply again." ] ] else text ""
viewError error = error |> Maybe.map (\e -> p [ Ui.errorText ] [ text e ]) |> Maybe.withDefault (text "")
approval model = if model.confirmingApproval then div [ Ui.confirmBox ] [ p [ Ui.confirmBoxQuestion ] [ strong [] [ text "This permanently deletes the raw recording. You won't be able to re-edit this sermon. Approve?" ] ], div [ Ui.confirmBoxButtons ] [ button [ Ui.dangerButton, onClick Approve, disabled model.applying ] [ text "Yes, Approve Permanently" ], button [ Ui.button, onClick CancelApproval ] [ text "Cancel" ] ] ] else text ""
backWarning model = if model.backWarning then div [ class "editor-warning" ] [ p [] [ strong [] [ text "You have unapplied changes. Your draft is saved in this browser." ] ], button [ Ui.button, onClick ForceClose ] [ text "Leave Editor" ], button [ Ui.button, onClick KeepEditing ] [ text "Keep Editing" ] ] else text ""
regionCard model index region = div [ class ("region-card" ++ if index == model.selected then " region-card--selected" else ""), onClick (Select index) ] [ div [ class "region-card__heading" ] [ h3 [] [ text (String.fromInt (index+1) ++ ". " ++ region.regionType) ], strong [] [ text (if region.keep then "KEPT" else "DELETED") ] ], p [] [ text (timestamp region.start ++ " – " ++ timestamp region.end) ], div [ class "region-card__controls" ] [ button [ Ui.button, onClick (Toggle index) ] [ text (if region.keep then "Delete Section" else "Keep Section") ], button [ Ui.button, onClick (Play index) ] [ text "Play Region" ] ], if index > 0 then nudgeControls index else text "", if index < List.length model.regions - 1 then nudgeControls (index+1) else text "" ]
nudgeControls boundary = div [ class "boundary-controls" ] [ span [] [ text ("Boundary " ++ String.fromInt boundary ++ ":") ], button [ Ui.button, onClick (Nudge boundary -1) ] [ text "−1s" ], button [ Ui.button, onClick (Nudge boundary -0.1) ] [ text "−0.1s" ], button [ Ui.button, onClick (Nudge boundary 0.1) ] [ text "+0.1s" ], button [ Ui.button, onClick (Nudge boundary 1) ] [ text "+1s" ] ]
timestamp seconds =
    let
        whole = round (seconds * 10)
        mins = whole // 600
        secs = toFloat (modBy 600 whole) / 10
    in
    String.fromInt mins ++ ":" ++ (if secs < 10 then "0" else "") ++ String.fromFloat secs
