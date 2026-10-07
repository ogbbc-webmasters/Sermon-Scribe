module Editing exposing (Model, Msg(..), init, isOpen, keptDuration, update, view)

import Browser.Dom
import Button
import Card
import Html exposing (Html, audio, button, div, input, label, p, span, strong, text)
import Html.Attributes exposing (attribute, checked, class, controls, disabled, id, name, src, type_)
import Html.Events exposing (on, onClick, preventDefaultOn, stopPropagationOn)
import Http
import Json.Decode as Decode
import Json.Encode as Encode
import Process
import Task
import Ui
import Url


type alias Breakpoint =
    { id : String, time : Float, kind : String }


type alias Section =
    { id : String, keep : Bool }


type alias Draft =
    { duration : Float, revision : Int, breakpoints : List Breakpoint, sections : List Section }


type Selection
    = Boundary Int
    | Passage Int


type alias Model =
    { sermonId : Maybe String
    , visible : Bool
    , draft : Maybe Draft
    , selection : Maybe Selection
    , saved : Maybe Draft
    , saving : Bool
    , regenerating : Bool
    , applying : Bool
    , pendingApply : Maybe Bool
    , error : Maybe String
    , audioStatus : String
    , playhead : Float
    , undo : List Draft
    , generation : Int
    , sequence : Int
    }


init : Model
init =
    Model Nothing False Nothing Nothing Nothing False False False Nothing Nothing "" 0 [] 0 0


type Msg
    = Open String
    | ApplyRecording String
    | Loaded Int (Result Http.Error Draft)
    | Close
    | Select Selection
    | Keep Int Bool
    | Nudge Float
    | AddBoundary
    | RemoveBoundary
    | Undo
    | SaveLater Int
    | RetrySave
    | Saved Int (Result Http.Error Draft)
    | Apply Bool
    | Applied Int (Result Http.Error ())
    | Reload
    | Regenerate
    | Regenerated Int (Result Http.Error Draft)
    | Preview
    | PlayFull
    | Playhead Float
    | AudioStatus String
    | Focused (Result Browser.Dom.Error ())
    | IgnoreClick


isOpen : String -> Model -> Bool
isOpen sermonId model =
    model.visible && model.sermonId == Just sermonId


endpoint : Model -> String
endpoint model =
    "/api/sermons/" ++ Url.percentEncode (Maybe.withDefault "" model.sermonId) ++ "/editing"


draftDecoder : Decode.Decoder Draft
draftDecoder =
    Decode.map4 Draft
        (Decode.field "duration" Decode.float)
        (Decode.field "revision" Decode.int)
        (Decode.field "breakpoints" (Decode.list (Decode.map3 Breakpoint (Decode.field "id" Decode.string) (Decode.field "time" Decode.float) (Decode.field "kind" Decode.string))))
        (Decode.field "sections" (Decode.list (Decode.map2 Section (Decode.field "id" Decode.string) (Decode.field "keep" Decode.bool))))


encodeDraft : Draft -> Encode.Value
encodeDraft draft =
    Encode.object
        [ ( "duration", Encode.float draft.duration )
        , ( "revision", Encode.int draft.revision )
        , ( "breakpoints", Encode.list (\b -> Encode.object [ ( "id", Encode.string b.id ), ( "time", Encode.float b.time ), ( "kind", Encode.string b.kind ) ]) draft.breakpoints )
        , ( "sections", Encode.list (\s -> Encode.object [ ( "id", Encode.string s.id ), ( "keep", Encode.bool s.keep ) ]) draft.sections )
        ]


effect : String -> List ( String, Encode.Value ) -> Maybe Encode.Value
effect action fields =
    Just (Encode.object (( "action", Encode.string action ) :: fields))


stop : Maybe Encode.Value
stop =
    effect "stop" []


update : Msg -> Model -> ( Model, Cmd Msg, Maybe Encode.Value )
update msg model =
    case msg of
        Open sermonId ->
            if model.sermonId == Just sermonId && model.draft /= Nothing then
                ( { model | visible = True }, Cmd.none, stop )

            else
                let
                    next =
                        { init | sermonId = Just sermonId, visible = True, generation = model.generation + 1 }
                in
                ( next, Http.get { url = endpoint next, expect = Http.expectJson (Loaded next.generation) draftDecoder }, stop )

        ApplyRecording sermonId ->
            if model.sermonId == Just sermonId && model.draft /= Nothing then
                update (Apply False) { model | visible = False }

            else
                let
                    ( next, cmd, audioEffect ) =
                        update (Open sermonId) model
                in
                ( { next | visible = False, pendingApply = Just False }, cmd, audioEffect )

        Loaded generation result ->
            if generation /= model.generation then
                ( model, Cmd.none, Nothing )

            else
                case result of
                    Ok draft ->
                        let
                            next =
                                { model | draft = Just draft, saved = Just draft, error = Nothing }
                        in
                        case next.pendingApply of
                            Just skip ->
                                apply skip next

                            Nothing ->
                                ( next, Cmd.none, Nothing )

                    Err (Http.BadStatus 404) ->
                        ( { model | error = Just "This recording has no edit draft yet. Close the editor and regenerate its transcription to enable editing.", pendingApply = Nothing }, Cmd.none, Nothing )

                    Err _ ->
                        ( { model | error = Just "Could not load editing suggestions. Try again.", pendingApply = Nothing }, Cmd.none, Nothing )

        Close ->
            ( { model | visible = False }, Cmd.none, stop )

        Reload ->
            update (Open (Maybe.withDefault "" model.sermonId)) { model | draft = Nothing }

        Regenerate ->
            case model.draft of
                Just draft ->
                    if model.saving || model.regenerating || model.applying || model.pendingApply /= Nothing || model.draft /= model.saved then
                        ( model, Cmd.none, Nothing )

                    else
                        ( { model | regenerating = True, error = Nothing }
                        , Http.post { url = endpoint model ++ "/regenerate", body = Http.jsonBody (Encode.object [ ( "revision", Encode.int draft.revision ) ]), expect = Http.expectJson (Regenerated model.generation) draftDecoder }
                        , stop
                        )

                Nothing ->
                    ( model, Cmd.none, Nothing )

        Regenerated generation result ->
            if generation /= model.generation then
                ( model, Cmd.none, Nothing )

            else
                case result of
                    Ok draft ->
                        ( { model | regenerating = False, draft = Just draft, saved = Just draft, selection = Nothing, undo = [], sequence = model.sequence + 1, audioStatus = "" }, Cmd.none, stop )

                    Err err ->
                        ( { model | regenerating = False, error = Just (saveError err) }, Cmd.none, Nothing )

        Select selection ->
            ( { model
                | selection =
                    if model.selection == Just selection then
                        Nothing

                    else
                        Just selection
                , audioStatus = ""
                , playhead = sectionStart selection model
              }
            , if model.selection == Just selection then
                Cmd.none

              else
                case selection of
                    Boundary _ ->
                        Task.attempt Focused (Browser.Dom.focus "breakpoint-adjustment")

                    Passage _ ->
                        Cmd.none
            , stop
            )

        IgnoreClick ->
            ( model, Cmd.none, Nothing )

        Focused _ ->
            ( model, Cmd.none, Nothing )

        Keep index keep ->
            change (\d -> { d | sections = replaceAt index (\s -> { s | keep = keep }) d.sections }) model

        Nudge delta ->
            case ( model.draft, model.selection ) of
                ( Just draft, Just (Boundary index) ) ->
                    case ( at index draft.breakpoints, at (index - 1) draft.breakpoints, at (index + 1) draft.breakpoints ) of
                        ( Just current, Just before, Just after ) ->
                            let
                                time =
                                    clamp (before.time + 0.05) (after.time - 0.05) (toFloat (round ((current.time + delta) * 1000)) / 1000)

                                ( next, cmd, _ ) =
                                    change (\d -> { d | breakpoints = replaceAt index (\b -> { b | time = time }) d.breakpoints }) model
                            in
                            ( next, cmd, preview next )

                        _ ->
                            ( model, Cmd.none, Nothing )

                _ ->
                    ( model, Cmd.none, Nothing )

        AddBoundary ->
            case ( model.draft, model.selection ) of
                ( Just draft, Just (Passage index) ) ->
                    case ( at index draft.breakpoints, at (index + 1) draft.breakpoints, at index draft.sections ) of
                        ( Just before, Just after, Just section ) ->
                            if model.playhead - before.time < 0.05 || after.time - model.playhead < 0.05 then
                                ( { model | error = Just "Play or seek inside the section before adding a breakpoint." }, Cmd.none, Nothing )

                            else
                                let
                                    unique =
                                        "manual-" ++ String.fromInt draft.revision ++ "-" ++ String.fromInt model.sequence

                                    ( next, cmd, audioEffect ) =
                                        change
                                            (\d ->
                                                { d
                                                    | breakpoints = List.take (index + 1) d.breakpoints ++ [ Breakpoint unique model.playhead "manual" ] ++ List.drop (index + 1) d.breakpoints
                                                    , sections = List.take (index + 1) d.sections ++ [ Section (unique ++ "-section") section.keep ] ++ List.drop (index + 1) d.sections
                                                }
                                            )
                                            model
                                in
                                ( { next | selection = Just (Boundary (index + 1)) }, Cmd.batch [ cmd, Task.attempt Focused (Browser.Dom.focus "breakpoint-adjustment") ], audioEffect )

                        _ ->
                            ( model, Cmd.none, Nothing )

                _ ->
                    ( model, Cmd.none, Nothing )

        RemoveBoundary ->
            case ( model.draft, model.selection ) of
                ( Just draft, Just (Boundary index) ) ->
                    case ( at (index - 1) draft.sections, at index draft.sections ) of
                        ( Just left, Just right ) ->
                            if left.keep /= right.keep then
                                ( { model | error = Just "Choose the same Keep / Delete setting on both sections before joining them." }, Cmd.none, Nothing )

                            else
                                let
                                    ( next, cmd, audioEffect ) =
                                        change (\d -> { d | breakpoints = removeAt index d.breakpoints, sections = removeAt index d.sections }) model
                                in
                                ( { next | selection = Just (Passage (index - 1)) }, cmd, audioEffect )

                        _ ->
                            ( model, Cmd.none, Nothing )

                _ ->
                    ( model, Cmd.none, Nothing )

        Undo ->
            case ( model.undo, model.draft ) of
                ( previous :: rest, Just draft ) ->
                    let
                        ( next, cmd, audioEffect ) =
                            change (\_ -> { previous | revision = draft.revision }) model
                    in
                    ( { next | undo = rest, selection = Nothing }, cmd, audioEffect )

                _ ->
                    ( model, Cmd.none, Nothing )

        SaveLater sequence ->
            if sequence == model.sequence then
                save model

            else
                ( model, Cmd.none, Nothing )

        RetrySave ->
            save { model | error = Nothing }

        Saved generation result ->
            if generation /= model.generation then
                ( model, Cmd.none, Nothing )

            else
                case result of
                    Ok saved ->
                        let
                            next =
                                { model | saving = False, saved = Just saved, draft = Maybe.map (\d -> { d | revision = saved.revision }) model.draft, error = Nothing }
                        in
                        if next.draft /= next.saved then
                            save next

                        else
                            case next.pendingApply of
                                Just skip ->
                                    apply skip next

                                Nothing ->
                                    ( next, Cmd.none, Nothing )

                    Err err ->
                        ( { model | saving = False, pendingApply = Nothing, error = Just (saveError err) }, Cmd.none, Nothing )

        Apply skip ->
            if model.regenerating then
                ( model, Cmd.none, Nothing )

            else if model.draft /= model.saved || model.saving then
                save { model | pendingApply = Just skip }

            else
                apply skip model

        Applied generation result ->
            if generation /= model.generation then
                ( model, Cmd.none, Nothing )

            else
                case result of
                    Ok _ ->
                        ( { init | generation = model.generation + 1 }, Cmd.none, stop )

                    Err err ->
                        ( { model | applying = False, pendingApply = Nothing, error = Just (saveError err) }, Cmd.none, Nothing )

        Preview ->
            ( { model | audioStatus = "Loading preview…" }, Cmd.none, preview model )

        PlayFull ->
            case ( model.draft, model.selection ) of
                ( Just draft, Just (Passage index) ) ->
                    case ( at index draft.breakpoints, at (index + 1) draft.breakpoints ) of
                        ( Just before, Just after ) ->
                            ( { model | audioStatus = "Playing full section" }, Cmd.none, effect "full" [ ( "start", Encode.float before.time ), ( "end", Encode.float after.time ) ] )

                        _ ->
                            ( model, Cmd.none, Nothing )

                _ ->
                    ( model, Cmd.none, Nothing )

        Playhead time ->
            ( { model | playhead = time }, Cmd.none, Nothing )

        AudioStatus status ->
            ( { model | audioStatus = status }, Cmd.none, Nothing )


saveError : Http.Error -> String
saveError err =
    case err of
        Http.BadStatus 409 ->
            "This draft changed in another window, or processing is running. Reload the saved draft before continuing."

        Http.BadBody message ->
            message

        _ ->
            "Could not save or apply your edits. Your changes are still here. Try again."


change : (Draft -> Draft) -> Model -> ( Model, Cmd Msg, Maybe Encode.Value )
change transform model =
    case model.draft of
        Just draft ->
            if model.regenerating || model.applying || model.pendingApply /= Nothing || transform draft == draft then
                ( model, Cmd.none, Nothing )

            else
                let
                    next =
                        { model | draft = Just (transform draft), undo = List.take 50 (draft :: model.undo), sequence = model.sequence + 1, error = Nothing, audioStatus = "" }
                in
                ( next, Task.perform (\_ -> SaveLater next.sequence) (Process.sleep 450), stop )

        Nothing ->
            ( model, Cmd.none, Nothing )


save : Model -> ( Model, Cmd Msg, Maybe Encode.Value )
save model =
    case model.draft of
        Just draft ->
            if model.saving || model.regenerating || model.applying || model.draft == model.saved then
                ( model, Cmd.none, Nothing )

            else
                ( { model | saving = True }
                , Http.request { method = "PUT", headers = [], url = endpoint model, body = Http.jsonBody (encodeDraft draft), expect = Http.expectJson (Saved model.generation) draftDecoder, timeout = Nothing, tracker = Nothing }
                , Nothing
                )

        Nothing ->
            ( model, Cmd.none, Nothing )


apply : Bool -> Model -> ( Model, Cmd Msg, Maybe Encode.Value )
apply skip model =
    case model.draft of
        Just draft ->
            ( { model | applying = True, pendingApply = Nothing, error = Nothing }
            , Http.post { url = endpoint model ++ "/apply", body = Http.jsonBody (Encode.object [ ( "revision", Encode.int draft.revision ), ( "skip", Encode.bool skip ) ]), expect = Http.expectStringResponse (Applied model.generation) applyResponse }
            , stop
            )

        Nothing ->
            ( model, Cmd.none, Nothing )


applyResponse : Http.Response String -> Result Http.Error ()
applyResponse response =
    case response of
        Http.GoodStatus_ _ _ ->
            Ok ()

        Http.BadStatus_ metadata body ->
            if metadata.statusCode == 409 then
                Err (Http.BadStatus 409)

            else
                Err (Http.BadBody (Decode.decodeString (Decode.field "error" Decode.string) body |> Result.withDefault "Could not apply edits. Please try again."))

        _ ->
            Err Http.NetworkError


preview : Model -> Maybe Encode.Value
preview model =
    case ( model.draft, model.selection ) of
        ( Just draft, Just (Boundary index) ) ->
            at index draft.breakpoints |> Maybe.andThen (\b -> effect "preview" [ ( "url", Encode.string (endpoint model ++ "/preview?mode=breakpoint&time=" ++ String.fromFloat b.time) ) ])

        ( Just draft, Just (Passage index) ) ->
            case ( at index draft.breakpoints, at (index + 1) draft.breakpoints ) of
                ( Just before, Just after ) ->
                    effect "preview" [ ( "url", Encode.string (endpoint model ++ "/preview?mode=section&start=" ++ String.fromFloat before.time ++ "&end=" ++ String.fromFloat after.time) ) ]

                _ ->
                    Nothing

        _ ->
            Nothing


at : Int -> List a -> Maybe a
at index list =
    if index < 0 then
        Nothing

    else
        List.head (List.drop index list)


replaceAt : Int -> (a -> a) -> List a -> List a
replaceAt index transform =
    List.indexedMap
        (\i item ->
            if i == index then
                transform item

            else
                item
        )


removeAt : Int -> List a -> List a
removeAt index list =
    List.take index list ++ List.drop (index + 1) list


sectionStart : Selection -> Model -> Float
sectionStart selection model =
    let
        index =
            case selection of
                Passage i ->
                    i

                Boundary i ->
                    i
    in
    model.draft |> Maybe.andThen (\d -> at index d.breakpoints) |> Maybe.map .time |> Maybe.withDefault 0


timestamp : Float -> String
timestamp time =
    let
        tenths =
            round (time * 10)

        seconds =
            tenths // 10
    in
    String.fromInt (seconds // 60) ++ ":" ++ String.padLeft 2 '0' (String.fromInt (modBy 60 seconds)) ++ "." ++ String.fromInt (modBy 10 tenths)


kindLabel : String -> String
kindLabel kind =
    case kind of
        "silence_start" ->
            "Silence starts"

        "silence_end" ->
            "Silence ends"

        "speaker" ->
            "Speaker segment"

        "singing_start" ->
            "Singing starts"

        "singing_end" ->
            "Singing ends"

        _ ->
            "Manual breakpoint"


view : Model -> Html Msg
view model =
    div [ class "editor" ]
        [ Card.viewWithSubtitle
            (text "Edit recording")
            (Maybe.map (\draft -> text ("Kept duration: " ++ timestamp (keptDuration draft))) model.draft)
            [ Button.view "button"
                (Button.regenerate "Regenerate breakpoints")
                model.regenerating
                [ onClick Regenerate
                , disabled (model.draft == Nothing || model.saving || model.regenerating || model.applying || model.pendingApply /= Nothing || model.draft /= model.saved)
                ]
            , Button.action "ph:arrow-right"
                "Continue"
                False
                [ onClick Close ]
            ]
            [ case model.error of
                Just error ->
                    div [ Ui.errorPanel, attribute "role" "alert" ]
                        [ p [] [ text error ]
                        , div [ Ui.sermonActions ]
                            [ if model.draft == Nothing then
                                Button.action "ph:x" "Close editor" False [ onClick Close ]

                              else
                                Button.action "ph:floppy-disk" "Try saving again" model.saving [ onClick RetrySave, disabled model.saving ]
                            , Button.action "ph:arrow-clockwise" "Reload saved draft" False [ onClick Reload, disabled (model.saving || model.regenerating || model.applying) ]
                            ]
                        ]

                Nothing ->
                    text ""
            , case model.draft of
                Nothing ->
                    p [ Ui.hint ]
                        [ text
                            (if model.error == Nothing then
                                "Loading sections…"

                             else
                                ""
                            )
                        ]

                Just draft ->
                    div [ class "editor__list" ] (List.concat (List.indexedMap (sectionRow model draft) draft.sections))
            ]
        ]


keptDuration : Draft -> Float
keptDuration draft =
    List.indexedMap
        (\i section ->
            if section.keep then
                Maybe.map2 (\a b -> b.time - a.time) (at i draft.breakpoints) (at (i + 1) draft.breakpoints) |> Maybe.withDefault 0

            else
                0
        )
        draft.sections
        |> List.sum


sectionRow : Model -> Draft -> Int -> Section -> List (Html Msg)
sectionRow model draft index section =
    let
        range =
            Maybe.map2 (\a b -> timestamp a.time ++ "–" ++ timestamp b.time) (at index draft.breakpoints) (at (index + 1) draft.breakpoints) |> Maybe.withDefault ""

        busy =
            model.regenerating || model.applying || model.pendingApply /= Nothing

        radios =
            div [ class "editor__choices", attribute "role" "group", attribute "aria-label" ("Section " ++ String.fromInt (index + 1) ++ " inclusion"), stopPropagationOn "click" (Decode.succeed ( IgnoreClick, True )) ]
                (List.map (\( keep, caption ) -> label [ class "editor__choice" ] [ input [ type_ "radio", name section.id, checked (section.keep == keep), disabled busy, onClick (Keep index keep) ] [], text caption ]) [ ( True, "Keep" ), ( False, "Delete" ) ])

        boundary =
            case at (index + 1) draft.breakpoints of
                Just b ->
                    if b.kind == "end" then
                        []

                    else
                        [ button
                            [ class "editor__breakpoint"
                            , onClick (Select (Boundary (index + 1)))
                            , disabled busy
                            , attribute "aria-expanded"
                                (if model.selection == Just (Boundary (index + 1)) then
                                    "true"

                                 else
                                    "false"
                                )
                            ]
                            [ text (timestamp b.time ++ " · " ++ kindLabel b.kind) ]
                        , if model.selection == Just (Boundary (index + 1)) then
                            boundaryPanel model draft (index + 1) b

                          else
                            text ""
                        ]

                Nothing ->
                    []
    in
    [ div
        [ class
            (if section.keep then
                "editor__section"

             else
                "editor__section editor__section--deleted"
            )
        ]
        [ div
            [ class "editor__row"
            , onClick
                (if busy then
                    IgnoreClick

                 else
                    Select (Passage index)
                )
            ]
            [ button
                [ class "editor__section-title"
                , disabled busy
                , attribute "aria-expanded"
                    (if model.selection == Just (Passage index) then
                        "true"

                     else
                        "false"
                    )
                ]
                [ Button.icon
                    (if model.selection == Just (Passage index) then
                        "ph:caret-down"

                     else
                        "ph:caret-right"
                    )
                , span [ class "editor__section-label" ]
                    [ strong [] [ text ("Section " ++ String.fromInt (index + 1)) ]
                    , span [] [ text range ]
                    ]
                ]
            , radios
            ]
        , if model.selection == Just (Passage index) then
            sectionPanel model draft index

          else
            text ""
        ]
    ]
        ++ boundary


boundaryPanel : Model -> Draft -> Int -> Breakpoint -> Html Msg
boundaryPanel model draft index b =
    let
        busy =
            model.regenerating || model.applying || model.pendingApply /= Nothing

        removable =
            Maybe.map2 (\left right -> left.keep == right.keep) (at (index - 1) draft.sections) (at index draft.sections) |> Maybe.withDefault False

        keyboard =
            preventDefaultOn "keydown"
                (Decode.map2
                    (\key shift ->
                        ( Nudge
                            (if key == "ArrowLeft" then
                                if shift then
                                    -1

                                else
                                    -0.1

                             else if shift then
                                1

                             else
                                0.1
                            )
                        , True
                        )
                    )
                    (Decode.field "key" Decode.string
                        |> Decode.andThen
                            (\key ->
                                if not busy && (key == "ArrowLeft" || key == "ArrowRight") then
                                    Decode.succeed key

                                else
                                    Decode.fail "not a nudge"
                            )
                    )
                    (Decode.field "shiftKey" Decode.bool)
                )
    in
    div [ class "editor__adjustment", id "breakpoint-adjustment", attribute "tabindex" "-1", attribute "aria-label" "Adjust breakpoint", keyboard ]
        [ strong [] [ text ("Place breakpoint · " ++ String.fromFloat (toFloat (round (b.time * 10)) / 10) ++ " seconds") ]
        , div [ Ui.sermonActions ]
            [ Button.action "ph:headphones" "Listen around breakpoint" False [ onClick Preview, disabled busy ]
            , Button.action "ph:arrow-left" "Earlier" False [ onClick (Nudge -0.1), disabled busy ]
            , Button.action "ph:arrow-right" "Later" False [ onClick (Nudge 0.1), disabled busy ]
            ]
        , Button.action "ph:minus" "Remove breakpoint" False [ onClick RemoveBoundary, disabled (busy || not removable) ]
        ]


sectionPanel : Model -> Draft -> Int -> Html Msg
sectionPanel model draft index =
    div [ class "editor__adjustment" ]
        [ div [ Ui.sermonActions ]
            [ Button.action "ph:headphones" "Preview start & finish" False [ onClick Preview ]
            , Button.action "ph:play" "Play full section" False [ onClick PlayFull ]
            ]
        , audio [ id "editing-source", class "editor__audio", controls True, attribute "preload" "metadata", attribute "data-start" (at index draft.breakpoints |> Maybe.map (.time >> String.fromFloat) |> Maybe.withDefault "0"), attribute "data-end" (at (index + 1) draft.breakpoints |> Maybe.map (.time >> String.fromFloat) |> Maybe.withDefault "0"), src ("/api/sermons/" ++ Url.percentEncode (Maybe.withDefault "" model.sermonId) ++ "/audio/source"), on "timeupdate" (Decode.at [ "target", "currentTime" ] Decode.float |> Decode.map Playhead) ] []
        , p [ attribute "role" "status", Ui.hint ] [ text model.audioStatus ]
        , div [ Ui.sermonActions ]
            [ Button.action "ph:plus" ("Add breakpoint at " ++ timestamp model.playhead) False [ onClick AddBoundary, disabled (model.applying || model.pendingApply /= Nothing) ]
            , Button.action "ph:skip-back" "Adjust start" False [ onClick (Select (Boundary index)), disabled (index == 0) ]
            , Button.action "ph:skip-forward" "Adjust finish" False [ onClick (Select (Boundary (index + 1))), disabled (index + 1 == List.length draft.breakpoints - 1) ]
            ]
        ]
